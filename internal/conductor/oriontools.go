package conductor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/revelara-ai/orion/internal/contextwindow"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/actuation"
	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/internal/hookbus"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/newbehavior"
	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// specTools exposes the deterministic spec pipeline as TOOLS the native Orion
// agent calls (the gates-as-tools inversion). The model reasons + grills; these
// tools are the only way it touches the store, and the completeness/compile/
// accept gates stay the deterministic truth source — the agent proposes, the
// gates verify (no agent grades its own homework).
func specTools(c *orchestrator.Conductor, provider llm.Provider, cs *changeSession, emit func(acp.Update)) *tools.Registry {
	r := tools.NewRegistry()
	// or-ykz.2: the generation-domain registry is the ONLY extensible tool
	// surface — installed packages intercept/rewrite/block calls here. The
	// proof domain never dispatches through an intercepted registry.
	r.SetIntercept(hookbus.Default.BeforeToolCall)
	registerChangeTools(r, cs, c, provider, emit)
	registerBeadsTool(r, c)
	registerMCPTools(r, c.Store())             // revelara.ai MCP tools, when authenticated (or-xe7.10)
	registerWorkspaceTools(r, c)               // bash + file I/O + search — general workspace agency (or-5j1)
	registerWebTools(r)                        // web_fetch + keyless web_search — web reach (or-5j1 slice 2)
	registerSubagentTool(r, c, provider, emit) // spawn_subagent — bounded nested delegation (or-5j1 slice 3)

	r.Register(tools.Tool{
		Name:        "submit_intent",
		Description: "Submit the developer's build intent (call once, first). Returns the open spec decisions to resolve.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"intent":{"type":"string","description":"the developer's stated goal"}},"required":["intent"]}`),
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Intent string `json:"intent"`
			}
			_ = json.Unmarshal(in, &p)
			conf, err := c.Submit(ctx, p.Intent)
			if err != nil {
				return "", err
			}
			out := asJSON(map[string]any{"message": conf.Message, "open_decisions": conf.OpenDecisions})
			// or-tcs.5: brownfield grilling is grounded AUTOMATICALLY — the
			// repo digest rides the submit result (and the project record),
			// so citing real packages never depends on the model remembering
			// to call read_codebase.
			if g := codebaseGrounding(ctx, c); g != "" {
				out += "\n\nCODEBASE GROUNDING (read from the repo — cite these REAL packages/APIs in your questions and the spec; do not invent structure):\n" + g
			}
			return out, nil
		},
	})

	r.Register(tools.Tool{
		Name: "amend_spec",
		Description: "Start a REFACTOR of an already-ratified spec: opens a new draft version SEEDED with the prior " +
			"spec's requirements and decisions (lineage recorded), so the developer edits instead of re-eliciting. " +
			"Use when the developer wants to change/extend what was already ratified. Edit with add_requirement/" +
			"remove_requirement/record_answer, then re-ratify with preview_spec → approve_assumptions → ratify_spec.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			av, err := c.AmendSpec(ctx)
			if err != nil {
				return "", err
			}
			return asJSON(av), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "check_completeness",
		Description: "List the spec decisions still open. Those with no fallback are BLOCKING — they must be answered before ratifying.",
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			sv, err := c.SpecView(ctx)
			if err != nil {
				return "", err
			}
			return asJSON(map[string]any{"status": sv.Status, "open_decisions": sv.OpenDecisions}), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "approve_assumptions",
		Description: "Record the developer's EXPLICIT confirmation of the open fallback assumptions (shown by preview_spec). Call this ONLY after the developer has seen each assumption and confirmed or overridden it — ratify_spec deterministically REFUSES while unapproved assumptions remain. Returns what was approved.",
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			approved, err := c.ApproveAssumptions(ctx)
			if err != nil {
				return "", err
			}
			if len(approved) == 0 {
				return "no open assumptions to approve", nil
			}
			return "approved with the developer: " + strings.Join(approved, ", "), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "read_codebase",
		Description: "Read the EXISTING codebase in the working directory: greenfield/brownfield mode, languages, key files, and (for Go) the package structure + exported API surface + internal dependency edges. Call this FIRST when the intent concerns an existing project, so your questions and the spec are grounded in the REAL code (which packages exist, what they expose, how they depend on each other) — not invented structure.",
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(_ context.Context, _ json.RawMessage) (string, error) {
			dir, err := os.Getwd()
			if err != nil {
				return "", err
			}
			m := brownfield.ScanRepoMap(dir)
			if m.Profile.Mode == brownfield.Greenfield {
				return "GREENFIELD workspace (" + dir + "): no existing source to integrate with — design new structure from the intent.", nil
			}
			return m.Digest(), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "map_domains",
		Description: "Analyze the existing codebase into its FUNCTIONAL domains (capabilities) and the packages implementing each — the semantic layer over read_codebase. Use it to locate WHERE the developer's intent lands (which domain/packages it touches). Proposed + grounded (every package validated against the real code); review it with the developer.",
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			if provider == nil {
				return "domain analysis needs a model provider (offline) — use read_codebase for the structural map instead.", nil
			}
			dir, err := os.Getwd()
			if err != nil {
				return "", err
			}
			m := brownfield.ScanRepoMap(dir)
			if m.Profile.Mode == brownfield.Greenfield {
				return "GREENFIELD workspace — no existing domains to map; design from the intent.", nil
			}
			fm, err := AnalyzeFunctionalModel(ctx, provider, m)
			if err != nil {
				return "", err
			}
			return fm.Digest(), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "propose_stamp_baseline",
		Description: "Propose a STAMP control-structure baseline for the EXISTING system — its losses, control structure (controllers + control actions + feedback), and unsafe control actions (each with the hazard + the code tokens that prove the control is present) — grounded in the functional model. This is the 'what must not break' baseline a brownfield change is later proven to PRESERVE. PROPOSED only; the developer ratifies each UCA (controlled / accepted-gap) before it anchors. Review it with the developer.",
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			if provider == nil {
				return "STAMP baseline needs a model provider (offline).", nil
			}
			dir, err := os.Getwd()
			if err != nil {
				return "", err
			}
			m := brownfield.ScanRepoMap(dir)
			if m.Profile.Mode == brownfield.Greenfield {
				return "GREENFIELD — no existing system to model; the new spec's STPA defines the hazards.", nil
			}
			fm, ferr := AnalyzeFunctionalModel(ctx, provider, m)
			if ferr != nil {
				return "", ferr
			}
			model, serr := AnalyzeSTAMPBaseline(ctx, provider, m, fm)
			if serr != nil {
				return "", serr
			}
			return RenderBaseline(model), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "direct_work",
		Description: "Map a developer's change intent onto the existing codebase's models to DIRECT the work: the functional model (which domains/packages it touches + the blast radius of impacted packages) AND the STAMP baseline (which control hazards the change must PRESERVE). Use this for a brownfield change to scope the decomposition and seed the proof obligations before grilling the details.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"intent":{"type":"string","description":"the developer's change intent"}},"required":["intent"]}`),
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			if provider == nil {
				return "directing work from intent needs a model provider (offline).", nil
			}
			var p struct {
				Intent string `json:"intent"`
			}
			_ = json.Unmarshal(in, &p)
			dir, err := os.Getwd()
			if err != nil {
				return "", err
			}
			m := brownfield.ScanRepoMap(dir)
			if m.Profile.Mode == brownfield.Greenfield {
				return "GREENFIELD — no existing codebase to map the intent against; grill the spec from the intent directly.", nil
			}
			fm, ferr := AnalyzeFunctionalModel(ctx, provider, m)
			if ferr != nil {
				return "", ferr
			}
			base, serr := AnalyzeSTAMPBaseline(ctx, provider, m, fm)
			if serr != nil {
				return "", serr
			}
			im, merr := MapIntent(ctx, provider, p.Intent, m, fm, base)
			if merr != nil {
				return "", merr
			}
			return im.Digest(), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "record_answer",
		Description: "Record the developer's answer to a spec decision (key from check_completeness + the value). For response_format, use a canonical value — \"json\" or \"plain text\" (the only formats the build+proof pipeline supports). If a tool returns an \"unrecognized/ambiguous format\" error, re-ask and record one of those.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"},"value":{"type":"string"}},"required":["key","value"]}`),
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct{ Key, Value string }
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if !c.DecisionKeys()[p.Key] {
				return "", fmt.Errorf("%q is not a spec decision key", p.Key)
			}
			if err := c.RecordAnswer(ctx, p.Key, p.Value); err != nil {
				return "", err
			}
			return "recorded " + p.Key + "=" + p.Value, nil
		},
	})

	r.Register(tools.Tool{
		Name:        "add_requirement",
		Description: "Record a behavioral requirement the developer stated, as STRUCTURED CASES. HTTP shape: {request:{...}, expect:{...}}. CLI shape: {kind:\"exec\", exec:{seed?, steps:[{argv:[\"$BIN\",...], expect:{exit?, stdout?, stderr?}}]}}. LIBRARY shape (or-v9f.23): {kind:\"unit\", unit:{pkg:\"storage\", steps:[{call:\"Put(\\\"k\\\",\\\"v\\\")\", want:\"error(nil)\"} | {call:..., want_err_re:\"missing.*name\"}]}} — call is a Go expression on the package's EXPORTED surface (wrap multi-returns: func() error { _, err := F(); return err }()); a step with restart:true crosses a REAL process boundary and requires modes_apply [\"empirical\"] + modes_rationale \"cross_process_persistence\". ARTIFACT shape: {kind:\"file\", file:{assertions:[{path, kind: exists|absent|contains|regex, value?}]}}. Each case becomes a proof obligation, so the build is held to it. NOT for test-only additions to an existing repo: use submit_change_intent -> build_change instead — the regression gate runs the new tests green-after, proving them without case capture.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"text":{"type":"string","description":"the behavior in one sentence"},
				"decision_key":{"type":"string","description":"the related decision key if any (e.g. timezone)"},
				"cases":{"type":"array","minItems":1,"items":{
					"type":"object",
					"properties":{
						"kind":{"type":"string","enum":["exec","unit","file"],"description":"omit for http; exec=CLI, unit=library surface, file=artifact tree"},
						"request":{"type":"object","properties":{"method":{"type":"string"},"path":{"type":"string"},"query":{"type":"object","additionalProperties":{"type":"string"}},"body":{"type":"string"}}},
						"expect":{"type":"object","properties":{
							"status":{"type":"integer"},
							"content_type":{"type":"string","enum":["application/json","text/plain"]},
							"assertions":{"type":"array","items":{"type":"object","properties":{
								"kind":{"type":"string","enum":["json_key_present","json_key_rfc3339","json_key_in_tz","json_error_present","body_rfc3339"]},
								"key":{"type":"string"},"value":{"type":"string","description":"e.g. an IANA timezone for json_key_in_tz"}},"required":["kind"]}}
						}},
						"unit":{"type":"object","properties":{
							"pkg":{"type":"string"},
							"steps":{"type":"array","minItems":1,"items":{"type":"object","properties":{
								"call":{"type":"string"},"want":{"type":"string"},"want_err_re":{"type":"string"},"restart":{"type":"boolean"}},"required":["call"]}}},"required":["steps"]},
						"file":{"type":"object","properties":{
							"assertions":{"type":"array","minItems":1,"items":{"type":"object","properties":{
								"path":{"type":"string"},"kind":{"type":"string","enum":["exists","absent","contains","regex"]},"value":{"type":"string"}},"required":["path","kind"]}}},"required":["assertions"]},
						"modes_apply":{"type":"array","items":{"type":"string","enum":["behavioral","empirical"]}},
						"modes_rationale":{"type":"string","enum":["cross_process_persistence"]},
						"exec":{"type":"object","properties":{
							"seed":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}},
							"steps":{"type":"array","minItems":1,"maxItems":1,"items":{"type":"object","properties":{
								"argv":{"type":"array","items":{"type":"string"},"minItems":1,"description":"argv[0] must be $BIN"},
								"stdin":{"type":"string"},
								"env":{"type":"object","additionalProperties":{"type":"string"}},
								"expect":{"type":"object","properties":{
									"exit":{"type":"integer"},
									"within_ms":{"type":"integer"},
									"stdout":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string","enum":["exact","contains","regex","empty","rfc3339_utc"]},"value":{"type":"string"}},"required":["kind"]}},
									"stderr":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string","enum":["exact","contains","regex","empty","rfc3339_utc"]},"value":{"type":"string"}},"required":["kind"]}}
								}}},"required":["argv","expect"]}}},"required":["steps"]}
					}}}
			},"required":["text","cases"]}`),
		Safety: tools.Safety{Destructive: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Text        string                `json:"text"`
				DecisionKey string                `json:"decision_key"`
				Cases       []spec.BehavioralCase `json:"cases"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			req := spec.Requirement{Source: completeness.DimFunctional, DecisionKey: p.DecisionKey, Text: p.Text, Cases: p.Cases}
			if err := c.AddRequirement(ctx, req); err != nil {
				// or-4j37: a rejection must TEACH — anchor diagnosis + the
				// closed union + a valid example + the test-only steer.
				return "", teachCaseShape(err, p.Cases)
			}
			return fmt.Sprintf("recorded requirement %q (%d case(s)) — it will be proven", p.Text, len(p.Cases)), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "list_requirements",
		Description: "List the structured behavioral requirements recorded so far, to review with the developer before ratifying.",
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			reqs, err := c.Requirements(ctx)
			if err != nil {
				return "", err
			}
			return asJSON(reqs), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "remove_requirement",
		Description: "Remove a behavioral requirement from the DRAFT spec by its id (full or a unique prefix, from list_requirements). The spec is EDITABLE, not append-only: use this when the developer revises or drops a requirement during review. To CHANGE a requirement, remove it then add_requirement the corrected one. (A scalar decision is revised by calling record_answer again — last write wins; you don't remove decisions.)",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"the requirement id to remove (full or unique prefix, from list_requirements)"}},"required":["id"]}`),
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if err := c.RemoveRequirement(ctx, p.ID); err != nil {
				return "", err
			}
			reqs, _ := c.Requirements(ctx)
			return fmt.Sprintf("removed requirement %s; %d requirement(s) remain", p.ID, len(reqs)), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "preview_spec",
		Description: "Assemble the spec WITHOUT accepting it (fallbacks resolved) and return it as a readable document for the developer to review. It surfaces an ASSUMPTIONS section — decisions resolved by a fallback default that the developer did NOT specify — which the developer should confirm or override before ratifying.",
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			es, err := c.PreviewSpec(ctx)
			if err != nil {
				return "", err
			}
			return SpecDocument(es, false), nil // readable preview incl. the assumptions to review
		},
	})

	r.Register(tools.Tool{
		Name:        "ratify_spec",
		Description: "Accept + anchor the spec. Call ONLY after the developer has reviewed it and confirmed it is correct. Returns the ratified spec DOCUMENT (Markdown) to show the developer.",
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			es, err := c.ApproveSpec(ctx)
			if err != nil {
				return "", err
			}
			doc := SpecDocument(es, true)
			// Persist the document as the durable artifact of the grill.
			if st := c.Store(); st != nil {
				dir := filepath.Join(st.Dir(), "specs")
				if err := os.MkdirAll(dir, 0o755); err == nil {
					_ = os.WriteFile(filepath.Join(dir, "spec-"+shortHash(es.Hash)+".md"), []byte(doc), 0o644)
				}
			}
			return "Ratified (anchor " + shortHash(es.Hash) + "). Spec document:\n\n" + doc, nil
		},
	})

	r.Register(tools.Tool{
		Name:        "build_service",
		Description: "Build the service to the ratified spec and PROVE it in one shot (decompose → generate → behavioral+empirical+hazard proof → reliability tier → deployment bar). Call after ratify_spec. Returns the proof verdict and delivery decision.",
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			st := c.Store()
			if st == nil {
				return "", fmt.Errorf("build requires a persistent store")
			}
			var phases []PhaseEvent
			// With a model provider, generate ARBITRARY code to the spec (general)
			// and audit alignment to intent; without one (offline/CI) fall back to
			// the deterministic fixture and skip alignment.
			var gen Generator
			var aligner Aligner
			if provider != nil {
				// or-kzf.4: every role resolves through the model router —
				// frontier-for-hard, cheap-for-easy, commodity default. The
				// effective routing is recorded on the project for audit.
				genProv := RoleProvider("generate", provider)
				gen = NativeGenerator(genProv, c.Budget(), st)
				aligner = NativeAligner(AlignJudgeProvider(RoleProvider("align", provider))) // or-kzf.1 env keeps precedence
				// or-809: give the plan path a semantic ModuleProposer (runs in
				// SHADOW behind ORION_MODULE_PROPOSER; the oracle still drives).
				c.SetModuleProposer(NativeModuleProposer(RoleProvider("propose", provider)))
				// or-7et.3: plan-time context-fit — a module that cannot fit
				// the window is split or escalated, never built.
				if es, eerr := c.RecallSpec(ctx); eerr == nil {
					c.SetModuleFitEstimator(NewModuleFitEstimator(genProv, es))
				}
				SetGenerationWindow(contextwindow.WindowOf(genProv, contextwindow.DefaultWindow))
				// or-794 (V3 Step 5): the open-ended grill drives elicitation
				// behind ORION_ELICITATION=grill; the checklist floor never drops.
				c.SetGrillAgent(NativeGrillAgent(RoleProvider("grill", provider)))
				// or-zn8: the adversarial issue-set reviewer rides the same brain;
				// the deterministic gate (advisory→ORION_ISSUE_REVIEW=block) decides.
				c.SetIssueReviewer(NativeIssueReviewer(RoleProvider("review", provider)))
				// or-gb1.4: the opt-in memory distillation pass rides the same
				// brain — inert unless ORION_MEMORY_DISTILL=1.
				SetDistillProvider(RoleProvider("distill", provider))
				// or-56c.2: the design-proof synthesis slot drafts a candidate
				// formal model at plan time; a human ratifies before it counts.
				c.SetModelSynthesizer(NativeModelSynthesizer(RoleProvider("design", provider)))
				RecordRoutingToStore(ctx, st)
			}
			res, err := BuildAndProve(ctx, st, gen, aligner, func(e PhaseEvent) {
				phases = append(phases, e)
				if emit != nil {
					emit(acp.Activity("Orion", e.Phase, 0, phaseStatusToActivity(e.Status)))
				}
			}, OutputRoot())
			if err != nil {
				return "", err
			}
			out := "Build pipeline:\n" + RenderPhaseReport(phases)
			out += fmt.Sprintf("\n\nVerdict %s · attempts %d · task closed=%v · tier %s · delivery %s.", res.Verdict, res.Attempts, res.Closed, res.Tier, res.Delivery)
			if res.OutputDir != "" {
				out += "\nCode written to: " + res.OutputDir + " (proven; visible in your working repo)"
			}
			if res.Git.Branch != "" {
				out += fmt.Sprintf("\nCommitted to branch %s (%s) — worktree: %s", res.Git.Branch, res.Git.Commit, res.Git.Path)
			}
			// or-tcs.7: surface the PR handoff over the feature branch.
			if res.PR.Opened {
				out += "\nPR opened: " + res.PR.URL
			} else if res.PR.ArtifactPath != "" {
				out += "\nPR-ready for review: " + res.PR.ArtifactPath
				if len(res.PR.Commands) > 0 {
					out += "\n  open it with:\n    " + strings.Join(res.PR.Commands, "\n    ")
				}
			}
			if res.Reason != "" {
				out += "\nEscalation: " + res.Reason
			}
			if res.Alignment.Ran && !res.Alignment.Aligned {
				out += fmt.Sprintf("\nAlignment (advisory, %s): %s", res.Alignment.Severity, res.Alignment.Concern)
			}
			// On a non-Accept verdict, surface the causal analysis so the developer sees
			// WHY it rejected (and what the refinement loop already tried to fix).
			if res.FailureAnalysis != "" {
				out += fmt.Sprintf("\n\nCausal analysis (after %d refinement attempt(s)):\n%s", res.Attempts, res.FailureAnalysis)
			}
			return out, nil
		},
	})

	r.Register(tools.Tool{
		Name:        "change_repo",
		Description: "Make a brownfield change to the EXISTING repo and PROVE it: generate the edit in a worktree off HEAD, prove it PRESERVES existing behavior (regression gate green-before→green-after), prove the asked-for change via ratified verification commands, and commit on a review branch only if both hold. Use for changes to an existing codebase — INCLUDING tooling/config changes that ship no service (e.g. add .golangci.yml + Makefile lint/vet targets). Not for greenfield (use build_service). The verify commands ARE the proof for a tooling change — author them yourself; the harness runs and judges them (you never grade your own work). Do NOT invent HTTP/service cases for a tooling change.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"intent":{"type":"string","description":"the developer's change intent"},` +
			`"verify":{"type":"array","description":"ratified verification commands proving the change did what was asked (the oracle). For tooling/config changes these ARE the proof.","items":{"type":"object","properties":{` +
			`"tool":{"type":"string","enum":["go","golangci-lint","file"],"description":"go/golangci-lint are executed under the sandbox; 'file' is a static no-exec assertion on a worktree file (e.g. a Makefile target)"},` +
			`"args":{"type":"array","items":{"type":"string"},"description":"for go/golangci-lint: argv after the tool. for 'file': args[0] is the worktree-relative path to assert on"},` +
			`"must_exit_zero":{"type":"boolean","description":"require exit 0 (target works / lint clean); ignored for 'file'"},` +
			`"config_validates":{"type":"boolean","description":"require positive proof the tool parsed the intended config"},` +
			`"config_ok_re":{"type":"string","description":"regexp that MUST match (the output for go/golangci-lint, the file content for 'file')"},` +
			`"config_fail_re":{"type":"string","description":"regexp that must NOT match (config-load failure, or a mis-wire for 'file')"},` +
			`"curate_golangci":{"type":"boolean","description":"for golangci-lint: vet the generated .golangci.yml into .orion-golangci.yml (reject plugins) before running; then pass --config .orion-golangci.yml"}` +
			`},"required":["tool","args"]}}` +
			`},"required":["intent"]}`),
		Safety: tools.Safety{Destructive: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Intent string `json:"intent"`
				Verify []struct {
					Tool            string   `json:"tool"`
					Args            []string `json:"args"`
					MustExitZero    bool     `json:"must_exit_zero"`
					ConfigValidates bool     `json:"config_validates"`
					ConfigOKRE      string   `json:"config_ok_re"`
					ConfigFailRE    string   `json:"config_fail_re"`
					CurateGolangci  bool     `json:"curate_golangci"`
				} `json:"verify"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if strings.TrimSpace(p.Intent) == "" {
				return "", fmt.Errorf("change_repo: intent is required")
			}
			if provider == nil {
				return "", fmt.Errorf("changing the repo needs a model provider (offline mode cannot generate edits)")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			root := GitRoot(ctx, cwd)
			if root == "" {
				return "", fmt.Errorf("not a git repository")
			}
			cases := make([]newbehavior.Case, 0, len(p.Verify))
			for _, v := range p.Verify {
				cases = append(cases, newbehavior.Case{Modality: "verify_command", Verify: &newbehavior.VerifyCommand{
					Tool: v.Tool, Args: v.Args, MustExitZero: v.MustExitZero,
					ConfigValidates: v.ConfigValidates, ConfigOKRE: v.ConfigOKRE, ConfigFailRE: v.ConfigFailRE,
					CurateGolangci: v.CurateGolangci,
				}})
			}
			res, cerr := ChangeAndProve(ctx, root, c.Store(), provider, p.Intent, cases, nil, phaseActivitySink(emit))
			if cerr != nil {
				return "", cerr
			}
			return renderChangeResult(p.Intent, res), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "git",
		Description: "Run a READ-ONLY git review operation in the developer's repo and return its output + exit code. Allowed: status, log, diff, show, rev-parse, ls-files, blame — plus ONE mutation: 'merge --ff-only <orion-… branch>' to LAND a PROVEN change_repo branch after the developer approves. Nothing else: commits happen ONLY through the proof pipeline (build_change / build_service), never via this tool, and push is not available (publishing is the developer's act). Landing rule: if --ff-only refuses (the base moved since the change was proven), the proof is stale — re-run change_repo off current HEAD, do NOT hand-merge.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"args":{"type":"array","items":{"type":"string"},"description":"git arguments after 'git', e.g. [\"merge\",\"--ff-only\",\"orion-change-...\"] or [\"diff\",\"main..orion-change-...\"]"}},"required":["args"]}`),
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Args []string `json:"args"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if len(p.Args) == 0 {
				return "", fmt.Errorf("git: args is required (the git arguments to run)")
			}
			// Fail-closed policy (or-4gib circumvention): a goal-driven model
			// WILL use whatever the tool card offers — the allowlist is code,
			// and the refusal teaches the proof path.
			if perr := gitPolicy(p.Args); perr != nil {
				return "", perr
			}
			cwd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			root := GitRoot(ctx, cwd)
			if root == "" {
				return "", fmt.Errorf("not a git repository")
			}
			// or-v9f.14: mutating git ops route through the deterministic actuation
			// gate; reads stay available so diagnosis works mid-halt.
			if !gitReadOnly(p.Args) {
				if gerr := storeRedButton(c).Guard("git " + p.Args[0]); gerr != nil {
					return "", gerr
				}
			}
			out, exit := gitRun(ctx, root, p.Args...)
			var b strings.Builder
			fmt.Fprintf(&b, "git %s (exit %d)\n", strings.Join(p.Args, " "), exit)
			if s := strings.TrimSpace(out); s != "" {
				b.WriteString(s)
			} else {
				b.WriteString("(no output)")
			}
			return b.String(), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "show_code",
		Description: "Report WHERE the proven code for the current spec lives in the developer's working repo and return its contents. Use this whenever the developer asks where the code is, to see it, or to answer questions about what was produced. Read-only.",
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			// Resolve active-or-last-delivered: after Accept the project leaves the
			// active slot (or-v9f.1), so a plain RecallSpec would wrongly report "no
			// proven spec" for the code we just wrote. RecallLastProvenSpec falls back
			// to the delivered spec so this answer stays truthful post-delivery.
			es, err := c.RecallLastProvenSpec(ctx)
			if err != nil {
				return "There is no accepted, proven spec yet — ratify a spec and build it (build_service); on Accept the code is written into your working repo.", nil
			}
			dir, files, lerr := locateProvenCode(es)
			if lerr != nil || len(files) == 0 {
				return fmt.Sprintf("No proven code on disk yet. When the ratified spec builds and proves Accept, the code is written to %s.", dir), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Proven code location: %s\n(%d files: %s)\n", dir, len(files), strings.Join(files, ", "))
			const perFileCap, totalCap = 6000, 24000
			for _, f := range files {
				if b.Len() > totalCap {
					b.WriteString("\n… (remaining files omitted; open the directory above to see them all)\n")
					break
				}
				data, rerr := os.ReadFile(filepath.Join(dir, f))
				if rerr != nil {
					continue
				}
				body := string(data)
				if len(body) > perFileCap {
					body = body[:perFileCap] + "\n… (truncated)"
				}
				fmt.Fprintf(&b, "\n===== %s =====\n%s\n", f, body)
			}
			return b.String(), nil
		},
	})

	// PTC (or-ykz.14): collapse long deterministic tool sequences into one call —
	// intermediates never enter the model's context. Registered LAST so the whole
	// registry is chainable; approval-requiring tools are refused per step.
	tools.RegisterChain(r)

	return r
}

func asJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// gitReadOnly reports whether a git invocation is on the read-only allowlist.
// Fail-safe: an unknown verb counts as mutating, so the red button over-blocks
// rather than under-blocks (or-v9f.14).
func gitReadOnly(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "status", "diff", "log", "show", "rev-parse", "ls-files", "ls-remote", "blame", "describe", "shortlog", "rev-list":
		return true
	}
	return false
}

// storeRedButton resolves the cross-process red button for the conductor's store
// (nil-safe: no store → a button that is never engaged).
func storeRedButton(c *orchestrator.Conductor) actuation.RedButton {
	if c == nil || c.Store() == nil {
		return actuation.RedButton{}
	}
	return actuation.RedButton{Path: filepath.Join(c.Store().Dir(), "red_button")}
}

// phaseStatusToActivity maps a PhaseStatus to an activity status string for
// streaming to the TUI activity panel.
// PhaseWarn maps to "warn" (advisory) — only PhaseFailed maps to "fail" (hard failure).
// The real pass/fail verdict lives in the build_report card, not the phase strip.
func phaseStatusToActivity(s PhaseStatus) string {
	switch s {
	case PhaseDone:
		return "done"
	case PhaseWarn:
		return "warn"
	case PhaseFailed:
		return "fail"
	default:
		return "running"
	}
}

// gitRun runs `git -C dir <args...>` and returns the combined output and exit code, WITHOUT
// turning a non-zero exit into a Go error — the `git` tool reports a failed op (e.g. a merge
// that isn't a fast-forward) back to the brain as readable output, not a tool error.
func gitRun(ctx context.Context, dir string, args ...string) (string, int) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	return string(out) + err.Error(), -1
}
