package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/revelara-ai/orion/internal/acp"
	"os"
	"strings"
	"sync"

	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/proof/newbehavior"
	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// changeSession is the in-session state of a brownfield CHANGE flow: the intent, the proposed/
// edited behavioral cases, and whether they've been ratified (the oracle gate). It bridges the
// change-flow tools across turns (specTools is rebuilt per turn, so the state cannot live in a
// tool closure) — one active change per session, mirroring the single active greenfield spec.
type changeSession struct {
	mu         sync.Mutex
	intent     string
	cases      []newbehavior.Case
	supersedes []string // existing tests whose old assertions this change intentionally voids
	ratified   bool
	// consumed (or-2l7): the ratified oracle was BUILT AND COMMITTED — spent.
	// 'ratified && !consumed' is a PENDING oracle a re-submit must not
	// silently discard (the post-compaction context-loss hazard); a consumed
	// one lets the next change open cleanly.
	consumed bool
}

// pending reports a ratified-but-unbuilt oracle (must not be silently discarded).
func (s *changeSession) pending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ratified && !s.consumed && len(s.cases) > 0
}

// pendingDigest renders the compact in-flight-change context re-injected
// after compaction, so the model keeps awareness of the unbuilt oracle.
func (s *changeSession) pendingDigest() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !(s.ratified && !s.consumed && len(s.cases) > 0) {
		return ""
	}
	ids := make([]string, 0, len(s.cases))
	for _, c := range s.cases {
		ids = append(ids, c.ID)
	}
	d := fmt.Sprintf("IN-FLIGHT CHANGE (ratified, not yet built): %s — %d ratified case(s): %s. Proceed with build_change; do NOT restart the flow.",
		s.intent, len(s.cases), strings.Join(ids, ", "))
	if len(s.supersedes) > 0 {
		d += " Supersedes: " + strings.Join(s.supersedes, ", ") + "."
	}
	return d
}

func (s *changeSession) snapshot() (string, []newbehavior.Case, []string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.intent, append([]newbehavior.Case(nil), s.cases...), append([]string(nil), s.supersedes...), s.ratified
}

// caseInput is the model-/developer-facing shape of one behavioral case (shared by propose_cases,
// add_case, and edit_case). ground() turns it into a newbehavior.Case or a reason it's rejected.
type caseInput struct {
	Modality     string     `json:"modality"`
	Pkg          string     `json:"pkg"`
	Call         string     `json:"call"`
	Want         string     `json:"want"`
	Setup        [][]string `json:"setup"`
	Assert       []string   `json:"assert"`
	ExpectExit   int        `json:"expect_exit"`
	ExpectStdout string     `json:"expect_stdout"`
}

// ground validates a proposed case against the real codebase + the modality contract, returning
// the case or a non-empty reason it was rejected (surfaced, never silently kept).
func (ci caseInput) ground(pkgDirs map[string]bool) (newbehavior.Case, string) {
	switch ci.Modality {
	case "synth_test":
		if ci.Pkg == "" || ci.Call == "" || ci.Want == "" {
			return newbehavior.Case{}, fmt.Sprintf("synth_test needs pkg+call+want (got pkg=%q call=%q want=%q)", ci.Pkg, ci.Call, ci.Want)
		}
		if !pkgDirs[ci.Pkg] {
			return newbehavior.Case{}, fmt.Sprintf("package %q does not exist in the repo", ci.Pkg)
		}
		return newbehavior.Case{Modality: "synth_test", Synth: &newbehavior.SynthTest{Pkg: ci.Pkg, Call: ci.Call, Want: ci.Want}}, ""
	case "command":
		if len(ci.Assert) == 0 {
			return newbehavior.Case{}, "command needs a non-empty assert argv"
		}
		return newbehavior.Case{Modality: "command", Command: &newbehavior.Command{
			Setup: ci.Setup, Assert: ci.Assert, ExpectExit: ci.ExpectExit, ExpectStdout: ci.ExpectStdout,
		}}, ""
	default:
		return newbehavior.Case{}, fmt.Sprintf("unknown modality %q (use synth_test or command)", ci.Modality)
	}
}

// renderCases lists the in-session cases for the developer to review.
func renderCases(cases []newbehavior.Case) string {
	if len(cases) == 0 {
		return "(no cases yet)"
	}
	var b strings.Builder
	for i, c := range cases {
		switch {
		case c.Synth != nil:
			fmt.Fprintf(&b, "  [%d] synth_test  %s: %s == %s\n", i, c.Synth.Pkg, c.Synth.Call, c.Synth.Want)
		case c.Command != nil:
			fmt.Fprintf(&b, "  [%d] command     %v (exit %d)\n", i, c.Command.Assert, c.Command.ExpectExit)
		case c.Verify != nil:
			fmt.Fprintf(&b, "  [%d] verify       %s %v\n", i, c.Verify.Tool, c.Verify.Args)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// registerChangeTools adds the brownfield change flow to the conductor surface, mirroring the
// greenfield build tools: submit_change_intent → propose_cases → add_case/edit_case →
// ratify_cases (oracle gate) → build_change. State lives in cs (per session).
func registerChangeTools(r *tools.Registry, cs *changeSession, c *orchestrator.Conductor, provider llm.Provider, emit func(acp.Update)) {
	repoMap := func(ctx context.Context) (string, brownfield.RepoMap, error) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", brownfield.RepoMap{}, err
		}
		root := GitRoot(ctx, cwd)
		if root == "" {
			return "", brownfield.RepoMap{}, fmt.Errorf("not a git repository")
		}
		return root, brownfield.ScanRepoMap(root), nil
	}

	r.Register(tools.Tool{
		Name:        "submit_change_intent",
		Description: "Open a brownfield CHANGE against the existing repo (a fix/refactor/addition to existing CODE — has runtime behavior). Returns the codebase map to ground the change. Flow: submit_change_intent → propose_cases → (add_case/edit_case) → ratify_cases → build_change. For a tooling/config change with NO Go behavior (linter config, Makefile), use change_repo directly instead.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"intent":{"type":"string","description":"the change to make"},"confirm_discard":{"type":"boolean","description":"set true to deliberately discard a pending (ratified, unbuilt) oracle"}},"required":["intent"]}`),
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Intent         string `json:"intent"`
				ConfirmDiscard bool   `json:"confirm_discard"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if strings.TrimSpace(p.Intent) == "" {
				return "", fmt.Errorf("submit_change_intent: intent is required")
			}
			// or-3p5.10: the mirror route — no existing code means nothing to
			// change; the intent belongs to the build flow.
			if dir, derr := os.Getwd(); derr == nil {
				if brownfield.Classify(dir).Mode == brownfield.Greenfield {
					return "ROUTED: this workspace has no existing source (greenfield) — the intent belongs to the BUILD flow. Call submit_intent with the same intent.", nil
				}
			}
			// or-2l7: after compaction the model may have forgotten a ratified
			// oracle awaiting build_change — never discard it silently.
			if cs.pending() && !p.ConfirmDiscard {
				cur, _, _, _ := cs.snapshot()
				return "", fmt.Errorf("submit_change_intent: a ratified-but-unbuilt oracle exists for %q — run build_change to build it, or re-submit with confirm_discard=true to deliberately discard it", cur)
			}
			_, m, err := repoMap(ctx)
			if err != nil {
				return "", err
			}
			cs.mu.Lock()
			cs.intent, cs.cases, cs.supersedes, cs.ratified, cs.consumed = p.Intent, nil, nil, false, false
			cs.mu.Unlock()
			return fmt.Sprintf("change intent recorded: %s\n\n%s\n\nNext: propose_cases to draft the behavioral proof oracle.", p.Intent, clip(m.Digest(), 4000)), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "propose_cases",
		Description: "Propose behavioral proof cases for the change (the ORACLE the proof checks): from the intent + codebase map, draft synth_test cases (assert a Go call's result: pkg+call+want) and/or command cases (run argv, assert exit+stdout). Each is GROUNDED against real packages; ungrounded proposals are dropped and surfaced. Review with the developer, refine via add_case/edit_case, then ratify_cases. Call after submit_change_intent.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			intent, _, _, ratified := cs.snapshot()
			if strings.TrimSpace(intent) == "" {
				return "", fmt.Errorf("propose_cases: call submit_change_intent first")
			}
			// Never re-draft (and silently discard) a ratified oracle — the trust gate
			// requires the oracle to predate the diff. After compaction the model may
			// have lost the in-context memory that cases were ratified; refuse and point
			// it to the intentional modify paths, which re-open ratification deliberately.
			if ratified {
				return "", fmt.Errorf("propose_cases: the behavioral oracle is already ratified — re-drafting would discard it. Use add_case/edit_case to revise (this re-opens ratification), or build_change to proceed with the ratified oracle")
			}
			if provider == nil {
				return "", fmt.Errorf("propose_cases needs a model provider")
			}
			_, m, err := repoMap(ctx)
			if err != nil {
				return "", err
			}
			cases, ungrounded, err := proposeCases(ctx, provider, intent, m)
			if err != nil {
				return "", err
			}
			cs.mu.Lock()
			cs.cases, cs.ratified = cases, false
			cs.mu.Unlock()
			out := fmt.Sprintf("proposed %d case(s) — review with the developer, then ratify_cases:\n%s", len(cases), renderCases(cases))
			if len(ungrounded) > 0 {
				out += "\n\ndropped (ungrounded):\n  - " + strings.Join(ungrounded, "\n  - ")
			}
			return out, nil
		},
	})

	addOrEdit := func(name, desc string, edit bool) {
		schema := `{"type":"object","properties":{
			"index":{"type":"integer","description":"0-based case index to replace"},
			"modality":{"type":"string","enum":["synth_test","command"]},
			"pkg":{"type":"string","description":"synth_test: package dir of the symbol under test (e.g. internal/foo)"},
			"call":{"type":"string","description":"synth_test: Go expression to evaluate (e.g. Verdict{...}.Severity())"},
			"want":{"type":"string","description":"synth_test: expected value as a Go literal (e.g. \"critical\")"},
			"setup":{"type":"array","items":{"type":"array","items":{"type":"string"}},"description":"command: argv steps before the assert"},
			"assert":{"type":"array","items":{"type":"string"},"description":"command: argv whose exit+stdout is the obligation"},
			"expect_exit":{"type":"integer"},
			"expect_stdout":{"type":"string"}
		},"required":["modality"]}`
		r.Register(tools.Tool{
			Name: name, Description: desc, InputSchema: json.RawMessage(schema),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var ci caseInput
				if err := json.Unmarshal(in, &ci); err != nil {
					return "", err
				}
				var idx struct {
					Index int `json:"index"`
				}
				_ = json.Unmarshal(in, &idx)
				_, m, err := repoMap(ctx)
				if err != nil {
					return "", err
				}
				pkgDirs := map[string]bool{}
				for _, pk := range m.Packages {
					pkgDirs[pk.Dir] = true
				}
				cse, why := ci.ground(pkgDirs)
				if why != "" {
					return "", fmt.Errorf("%s: %s", name, why)
				}
				cs.mu.Lock()
				defer cs.mu.Unlock()
				if edit {
					if idx.Index < 0 || idx.Index >= len(cs.cases) {
						return "", fmt.Errorf("edit_case: index %d out of range (have %d cases)", idx.Index, len(cs.cases))
					}
					cs.cases[idx.Index] = cse
				} else {
					cs.cases = append(cs.cases, cse)
				}
				cs.ratified = false
				return fmt.Sprintf("cases now (ratification re-opened):\n%s", renderCases(cs.cases)), nil
			},
		})
	}
	addOrEdit("add_case", "Add one behavioral proof case the propose_cases pass missed. Re-opens ratification.", false)
	addOrEdit("edit_case", "Replace the behavioral case at 'index' (0-based) with refined fields. Re-opens ratification.", true)

	r.Register(tools.Tool{
		Name:        "supersede_test",
		Description: "Declare an existing test whose OLD assertion this change INTENTIONALLY voids — for a behavior you are DELIBERATELY changing. The regression gate SKIPS it (so the intended change isn't blocked as a 'regression'), while every OTHER test must still pass AND the new behavior must be covered by a ratified case. Use ONLY for behavior you are deliberately changing — never to silence a real regression. The value is a Go test name or regexp (e.g. TestSeverity).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"test":{"type":"string","description":"a go test name or -skip regexp whose old behavior this change supersedes"}},"required":["test"]}`),
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Test string `json:"test"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if strings.TrimSpace(p.Test) == "" {
				return "", fmt.Errorf("supersede_test: a test name/regexp is required")
			}
			cs.mu.Lock()
			cs.supersedes = append(cs.supersedes, strings.TrimSpace(p.Test))
			cs.ratified = false
			list := strings.Join(cs.supersedes, ", ")
			cs.mu.Unlock()
			return fmt.Sprintf("superseded (regression-skipped) tests now: %s — the new behavior must still be a ratified case. Ratification re-opened.", list), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "ratify_cases",
		Description: "Lock the behavioral cases as the proof ORACLE, BEFORE any code is generated — the trust gate: the oracle predates the diff, so the proof is independent of the generated code. Call once the developer has reviewed and confirmed the cases. For a TEST-ONLY or purely additive change where the regression gate itself is a sufficient oracle (the new tests must compile and pass in green-after), skip case drafting and call ratify_cases with {\"regression_only\": true}. Then call build_change.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"regression_only":{"type":"boolean","description":"ratify WITHOUT behavioral cases: the regression gate (do-no-harm, which runs any newly added tests) is the oracle. For test-only/additive changes; CLI parity with 'orion change' without --cases."}}}`),
		Safety:      tools.Safety{Destructive: true, RequiresApproval: true}, // consent-recording: the human gate (or-7xw1)
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			var p struct {
				RegressionOnly bool `json:"regression_only"`
			}
			_ = json.Unmarshal(in, &p)
			cs.mu.Lock()
			defer cs.mu.Unlock()
			if p.RegressionOnly && len(cs.cases) > 0 {
				return "", fmt.Errorf("ratify_cases: regression_only conflicts with %d drafted case(s) — ratify the cases, or remove them first (the oracle must be unambiguous)", len(cs.cases))
			}
			if !p.RegressionOnly && len(cs.cases) == 0 {
				return "", fmt.Errorf("ratify_cases: no cases to ratify. Draft cases with propose_cases/add_case — or, for a TEST-ONLY or purely additive change where the new tests themselves are the proof, call ratify_cases with {\"regression_only\": true} to proceed with the regression gate as the oracle")
			}
			cs.ratified = true
			if p.RegressionOnly {
				return "ratified with NO behavioral cases: the regression gate is the oracle (do-no-harm must hold, and any newly added tests must compile and pass in green-after). Call build_change to generate + prove.", nil
			}
			return fmt.Sprintf("ratified %d case(s) as the proof oracle. Call build_change to generate + prove.", len(cs.cases)), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "finish_change",
		Description: "The SINGLE post-proof step for a PROVEN change branch: fast-forward it onto the developer's current base, close the beads issue it resolves (optional issue_id — verified before closing), and reclaim the worktree + branch. Call ONCE after the developer's one approval — never run separate merge/close/cleanup rounds. Refuses (and teaches) when the base moved since the proof: re-run the change flow instead of hand-merging.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"branch":{"type":"string","description":"the proven orion-change-… review branch"},"issue_id":{"type":"string","description":"the beads issue this change resolves, if any"}},"required":["branch"]}`),
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Branch  string `json:"branch"`
				IssueID string `json:"issue_id"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			root := GitRoot(ctx, ".")
			if root == "" {
				return "", fmt.Errorf("not in a git repo")
			}
			return LandProvenChange(ctx, root, c.Store(), storeRedButton(c), p.Branch, p.IssueID)
		},
	})

	r.Register(tools.Tool{
		Name:        "build_change",
		Description: "Generate the change and PROVE it against the RATIFIED cases: regression gate (do-no-harm) + new-behavior proof (the ratified oracle) → commit on a review branch ONLY if both hold. Call after ratify_cases. Reports the verdict + per-obligation transcript; if NOT committed, read the do-no-harm transcript digest in the result, fix the defect it names, and call build_change again — failure→fix→retry is the expected loop (fresh worktree per attempt). Escalate to the developer only if the same defect survives two corrected attempts.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"force_rederive":{"type":"boolean","description":"skip reuse of an existing proven artifact and regenerate fresh (deliberate retry)"}}}`),
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				ForceRederive bool `json:"force_rederive"`
			}
			_ = json.Unmarshal(in, &p)
			if p.ForceRederive {
				ctx = WithForceRederive(ctx)
			}
			intent, cases, supersedes, ratified := cs.snapshot()
			if !ratified {
				return "", fmt.Errorf("build_change: ratify_cases first (the oracle must be locked before generation)")
			}
			if provider == nil {
				return "", fmt.Errorf("build_change needs a model provider to generate the change")
			}
			root, _, err := repoMap(ctx)
			if err != nil {
				return "", err
			}
			res, cerr := ChangeAndProve(ctx, root, c.Store(), provider, intent, cases, supersedes, phaseActivitySink(emit))
			if cerr != nil {
				return "", cerr
			}
			// or-2l7: a COMMITTED build spends the oracle — the next
			// submit_change_intent opens cleanly instead of tripping the
			// pending-oracle guard. A failed build stays pending (retry loop).
			if res.Committed {
				cs.mu.Lock()
				cs.consumed = true
				cs.mu.Unlock()
			}
			return renderChangeResult(intent, res), nil
		},
	})
}

// renderChangeResult formats a ChangeAndProve outcome for the developer — shared by build_change
// and change_repo. It surfaces the per-obligation verify transcript so a NOT-committed result
// names which check failed and why, instead of leaving the brain to guess.
func renderChangeResult(intent string, res ChangeResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "change: %s\n  branch: %s\n", intent, res.Branch)
	if len(res.FilesChanged) > 0 {
		fmt.Fprintf(&b, "  files: %s\n", strings.Join(res.FilesChanged, ", "))
	}
	fmt.Fprintf(&b, "  regression: do-no-harm held=%v\n", res.Regression.Held)
	// or-cp90: the delta verdict's evidence — excluded pre-existing failures are
	// named honestly (never a silent pass), and new failures name the blockers.
	if n := len(res.Regression.PreExisting); n > 0 {
		fmt.Fprintf(&b, "  pre-existing failures excluded from do-no-harm (%d): %s\n", n, strings.Join(res.Regression.PreExisting, ", "))
	}
	if len(res.Regression.NewFailures) > 0 {
		fmt.Fprintf(&b, "  NEW failures introduced by the change: %s\n", strings.Join(res.Regression.NewFailures, ", "))
	}
	if d := res.FailureDigest(); d != "" {
		// The failing run's evidence goes INTO the tool result so the model can
		// self-correct (fix the diff / the cases) instead of asking a human for
		// a transcript it can't reach (or-67av).
		fmt.Fprintf(&b, "  do-no-harm transcript (digest):\n    %s\n", strings.ReplaceAll(d, "\n", "\n    "))
	}
	if res.Regression.Scope != "" {
		fmt.Fprintf(&b, "  scope: %s\n", res.Regression.Scope)
	}
	if res.NewBehavior != nil {
		fmt.Fprintf(&b, "  verification: pass=%v inconclusive=%v\n", res.NewBehavior.Pass, res.NewBehavior.Inconclusive)
		for _, line := range strings.Split(strings.TrimSpace(res.NewBehavior.Output), "\n") {
			if strings.TrimSpace(line) != "" {
				fmt.Fprintf(&b, "    %s\n", line)
			}
		}
	}
	if res.Committed {
		fmt.Fprintf(&b, "  COMMITTED on %s (review: git diff main..%s)\n", res.Branch, res.Branch)
		if res.Tier != "" {
			fmt.Fprintf(&b, "  tier: %s\n", res.Tier)
		}
		if res.PR.ArtifactPath != "" { // or-v9f.15: PR-ready handoff over the review branch
			if res.PR.Opened {
				fmt.Fprintf(&b, "  PR opened: %s\n", res.PR.URL)
			} else {
				fmt.Fprintf(&b, "  PR-ready: %s\n", res.PR.ArtifactPath)
			}
		}
	} else {
		fmt.Fprintf(&b, "  NOT committed — %s\n", res.Reason)
		// The retry affordance lives HERE, next to the failure, not only in the
		// system prompt: small models act on proximal instructions, and without
		// this they diagnose the digest correctly and then give up (gemma,
		// or-4gib). Failure→fix→retry is the intended loop.
		fmt.Fprintf(&b, "  next: this is a normal, recoverable outcome — fix the defect named above and call build_change again with a corrected intent. Each attempt runs in a fresh worktree, so retrying is safe and expected. Do not hand the fix back to the developer unless the same defect survives two corrected attempts.\n")
		if res.EscalationID != "" { // or-v9f.15: actionable via the unified inbox
			fmt.Fprintf(&b, "  escalation: %s (orion escalations resolve %s)\n", res.EscalationID, res.EscalationID)
		}
	}
	return b.String()
}

// proposeCases asks the coordinator model to draft behavioral cases from the intent + repo map,
// then DETERMINISTICALLY grounds each (real package, well-formed modality). The proposer is a
// coordinator step distinct from the generator (DiffGenerator); ratification happens before
// generation, so the oracle is independent of the generated code by construction.
func proposeCases(ctx context.Context, provider llm.Provider, intent string, m brownfield.RepoMap) ([]newbehavior.Case, []string, error) {
	tool := llm.Tool{
		Name:        "propose_behavioral_cases",
		Description: "Propose the behavioral proof cases that verify the change did what was asked.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{
			"cases":{"type":"array","items":{"type":"object","properties":{
				"modality":{"type":"string","enum":["synth_test","command"]},
				"pkg":{"type":"string","description":"synth_test: package dir of the symbol under test (e.g. internal/foo)"},
				"call":{"type":"string","description":"synth_test: Go expression to evaluate (e.g. Verdict{Failures:1}.Severity())"},
				"want":{"type":"string","description":"synth_test: expected value as a Go literal (e.g. \"critical\")"},
				"setup":{"type":"array","items":{"type":"array","items":{"type":"string"}}},
				"assert":{"type":"array","items":{"type":"string"}},
				"expect_exit":{"type":"integer"},
				"expect_stdout":{"type":"string"}
			},"required":["modality"]}}
		},"required":["cases"]}`),
	}
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		System:   proposeCasesPrompt,
		Tools:    []llm.Tool{tool},
		Messages: []llm.Message{llm.TextMessage(llm.RoleUser, renderProposeTask(intent, m))},
	})
	if err != nil {
		return nil, nil, err
	}
	pkgDirs := map[string]bool{}
	for _, p := range m.Packages {
		pkgDirs[p.Dir] = true
	}
	var cases []newbehavior.Case
	var ungrounded []string
	for _, tu := range resp.ToolUses() {
		if tu.Name != "propose_behavioral_cases" {
			continue
		}
		var p struct {
			Cases []caseInput `json:"cases"`
		}
		if err := json.Unmarshal(tu.Input, &p); err != nil {
			return nil, nil, err
		}
		for _, ci := range p.Cases {
			cse, why := ci.ground(pkgDirs)
			if why != "" {
				ungrounded = append(ungrounded, why)
				continue
			}
			cases = append(cases, cse)
		}
	}
	return cases, ungrounded, nil
}

const proposeCasesPrompt = `You propose BEHAVIORAL PROOF CASES for a brownfield change to an existing Go repo — the oracle an independent harness will check after the change is generated. You do NOT write the change.
Rules:
- One case per distinct behavior/branch the change must exhibit (e.g. a method returning critical|warn|ok → one case per return value).
- Prefer synth_test: pkg = the package dir of the symbol, call = a Go expression that exercises the new behavior, want = the expected value as a Go literal (quote strings).
- Use command only for a binary/CLI/endpoint behavior (argv + expected exit/stdout).
- Ground every case in REAL packages from the codebase map; name the package by its dir. Do not invent packages or symbols that aren't implied by the intent + map.
- Propose the minimal set that fully pins the asked-for behavior. Return them via propose_behavioral_cases.`

func renderProposeTask(intent string, m brownfield.RepoMap) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Change intent\n%s\n\n", strings.TrimSpace(intent))
	b.WriteString(clip(m.Digest(), 6000))
	return b.String()
}
