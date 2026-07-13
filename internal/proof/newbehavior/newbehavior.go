// Package newbehavior is the brownfield NEW-BEHAVIOR proof (or-3p5.3). Where the
// regression gate proves a change does no harm (existing tests stay green), this
// proves the change does what was asked — against ratified behavioral cases whose
// expected result (the oracle) is the case itself, never the generator. The harness
// authors and runs every check; the generated code is exercised only here.
//
// Slice 1a implements the synth_test modality: the harness synthesizes a plain-Go
// call/assert test in the changed package and runs it. The command modality (build +
// loopback curl / CLI) is slice 1b (or-3p5.8). Design:
// docs/SPEC/Orion-NewBehavior-Proof-Design.md.
//
// Isolation matches the brownfield regression gate (internal/brownfield): tests run
// under safeenv (host secrets scrubbed) so the existing module's deps resolve via the
// host module cache. Full bwrap sandboxing of brownfield change execution is a separate
// hardening — the regression gate doesn't bwrap either.
package newbehavior

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/proofexec"
	"github.com/revelara-ai/orion/internal/proof/safeenv"
	"github.com/revelara-ai/orion/internal/proof/toolingcfg"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// SynthTest is the synth_test modality payload: prove a func/method in the changed
// package. Call is a Go expression evaluated IN the package (no package qualifier, so
// it may reach unexported symbols); Want is the expected value as a Go literal.
type SynthTest struct {
	Pkg  string // package dir relative to the artifact (module) root
	Call string // e.g. `Verdict{OK:true,Reason:"x"}.String()`
	Want string // e.g. `"OK: x"`
}

// Command is the command modality payload (slice 1b): prove a binary/endpoint/CLI by
// running ratified argv and asserting the exit code + stdout. Setup steps run first
// (e.g. `go build`); the Assert argv's exit/stdout is the obligation. ExpectStdout is a
// substring, or a regexp when wrapped in /slashes/; an empty ExpectStdout checks only the
// exit code.
type Command struct {
	Setup        [][]string
	Assert       []string
	ExpectExit   int
	ExpectStdout string
}

// VerifyCommand is the verify_command modality: prove a DECLARED verification command (e.g.
// `golangci-lint config verify`, `make -n lint`, `go vet ./...`) runs and demonstrably did what
// it claimed — for changes that ship NO runnable service (tooling/config: linter config,
// Makefile targets, CI). The argv is RATIFIED INPUT (the oracle), authored by the conductor and
// never inferred from the generated files; it runs under the allowlisted, sandboxed verifier
// (proofexec.RunTool — go | golangci-lint | make -n only, secret-scrubbed, net+FS denied).
// Unlike Command (a built binary's runtime behavior), this proves a tooling obligation.
type VerifyCommand struct {
	Tool string   // "go" | "golangci-lint" (executed, sandboxed) | "file" (static assertion, no exec)
	Args []string // for go/golangci-lint: argv[1:]. for "file": Args[0] is a worktree-relative path

	MustExitZero bool // when set, pass requires exit == 0 (target works / lint clean)

	// ConfigValidates, when set, requires POSITIVE evidence the tool parsed + used the intended
	// config (not a silent default fallback): ConfigOKRE MUST match the combined output and
	// ConfigFailRE must NOT. Checked INDEPENDENTLY of the exit code, so a tool that ran clean
	// while ignoring the config does NOT pass.
	ConfigValidates bool
	ConfigOKRE      string // regexp that MUST match stdout+stderr
	ConfigFailRE    string // regexp that must NOT match (config-load failure)

	// CurateGolangci, when set, curates the worktree's generated .golangci.yml to an
	// Orion-controlled .orion-golangci.yml (rejecting plugin/custom-linter keys) BEFORE the
	// command runs — so a command that passes `--config .orion-golangci.yml` reads a vetted copy,
	// never the generated file picked up from the CWD. A rejected/invalid config makes the
	// obligation Executed=false (the change is not certified).
	CurateGolangci bool
}

// Case is one ratified new-behavior obligation.
type Case struct {
	ID       string // content-addressed; derived from the payload when empty
	Modality string // "synth_test" | "command" | "verify_command"
	Synth    *SynthTest
	Command  *Command
	Verify   *VerifyCommand
}

const synthTestFile = "orion_newbehavior_test.go"

// withID returns the case with a content-addressed ID when one is not set.
func (c Case) withID() Case {
	if c.ID != "" {
		return c
	}
	var payload string
	switch {
	case c.Synth != nil:
		payload = "synth\x00" + c.Synth.Pkg + "\x00" + c.Synth.Call + "\x00" + c.Synth.Want
	case c.Command != nil:
		payload = "command\x00" + strings.Join(c.Command.Assert, " ") + "\x00" + c.Command.ExpectStdout + "\x00" + strconv.Itoa(c.Command.ExpectExit)
	case c.Verify != nil:
		v := c.Verify
		payload = "verify\x00" + v.Tool + "\x00" + strings.Join(v.Args, " ") + "\x00" +
			strconv.FormatBool(v.MustExitZero) + "\x00" + strconv.FormatBool(v.ConfigValidates) + "\x00" +
			v.ConfigOKRE + "\x00" + v.ConfigFailRE + "\x00" + strconv.FormatBool(v.CurateGolangci)
	}
	h := sha256.Sum256([]byte(c.Modality + "\x00" + payload))
	c.ID = hex.EncodeToString(h[:])[:12]
	return c
}

// ProveNewBehavior proves the ratified cases against the changed artifact in
// artifactDir and returns a "new_behavior" ModeResult (per-case obligations + verdict).
// The verdict is Accept (Pass) only if every case Executed AND Passed; an empty/never-run
// set is Inconclusive (a coverage hole, not a silent pass).
func ProveNewBehavior(ctx context.Context, artifactDir string, cases []Case) (truthalign.ModeResult, error) {
	mr := truthalign.ModeResult{Mode: "new_behavior", Obligations: map[string]truthalign.ObligationStatus{}}

	// synth_test cases are grouped by package (one synthesized file + one `go test` per
	// pkg); command cases are run directly here.
	byPkg := map[string][]Case{}
	var ids []string
	var out strings.Builder
	for _, c := range cases {
		switch c.Modality {
		case "synth_test":
			if c.Synth == nil {
				continue
			}
			c = c.withID()
			byPkg[c.Synth.Pkg] = append(byPkg[c.Synth.Pkg], c)
			ids = append(ids, c.ID)
		case "command":
			if c.Command == nil {
				continue
			}
			c = c.withID()
			ids = append(ids, c.ID)
			st, diag := proveCommand(ctx, artifactDir, *c.Command)
			mr.Obligations[c.ID] = st
			fmt.Fprintf(&out, "command %s: %s\n", c.ID, diag)
		case "verify_command":
			if c.Verify == nil {
				continue
			}
			c = c.withID()
			ids = append(ids, c.ID)
			st, diag := proveVerify(ctx, artifactDir, *c.Verify)
			mr.Obligations[c.ID] = st
			fmt.Fprintf(&out, "verify %s: %s\n", c.ID, diag)
		}
	}

	for pkg, pcases := range byPkg {
		pkgDir := filepath.Join(artifactDir, pkg)
		pkgName, err := packageName(pkgDir)
		if err != nil {
			return truthalign.ModeResult{}, err
		}
		file := filepath.Join(pkgDir, synthTestFile)
		if err := os.WriteFile(file, []byte(synthSource(pkgName, pcases)), 0o644); err != nil {
			return truthalign.ModeResult{}, fmt.Errorf("write synth test: %w", err)
		}
		// Defer-remove so the proof artifact never leaks into the committed change.
		output := func() string {
			defer func() { _ = os.Remove(file) }()
			o, _ := runGoTest(ctx, artifactDir, pkg) // a failing/non-compiling run -> no markers
			return o
		}()
		out.WriteString(output)
		for id, st := range parseObligations(output) {
			mr.Obligations[id] = st
		}
	}
	mr.Output = out.String()

	if len(ids) == 0 {
		mr.Inconclusive = true // nothing to prove (no synth_test cases)
		return mr, nil
	}
	pass := true
	for _, id := range ids {
		st := mr.Obligations[id]
		if !st.Executed || !st.Passed {
			pass = false
		}
	}
	mr.Pass = pass
	return mr, nil
}

// runGoTest runs the synthesized obligations in pkg under safeenv (host secrets
// scrubbed; module cache available so the existing repo's deps resolve), from the
// module root. -v keeps the obligation markers from being suppressed; -run targets only
// the synthesized funcs so the package's own tests are not re-run here.
func runGoTest(ctx context.Context, moduleRoot, pkg string) (string, error) {
	// or-tf8 H3: synth_test executes GENERATED code — route it through the
	// proof-exec sandbox (bwrap namespace, scrubbed env, network denied,
	// host module cache read-only) instead of bare safeenv.
	out, code, err := proofexec.GoToolchain(ctx, moduleRoot, "test", "-v", "-run", "Test_orionNB_", "./"+filepath.ToSlash(pkg))
	if err != nil {
		return out, err
	}
	if code != 0 {
		return out, fmt.Errorf("go test exited %d", code)
	}
	return out, nil
}

// proveCommand runs the command modality's Setup steps then the Assert argv under safeenv
// (same isolation as the brownfield regression gate — secrets scrubbed, module cache
// available; loopback-only bwrap networking is a later hardening), and reports whether the
// Assert's exit code and stdout satisfy the ratified expectation. A failed Setup is
// executed-but-not-passed (the change could not even be exercised).
func proveCommand(ctx context.Context, dir string, c Command) (truthalign.ObligationStatus, string) {
	for _, s := range c.Setup {
		if stdout, stderr, exit := runArgv(ctx, dir, s); exit != 0 {
			return truthalign.ObligationStatus{Executed: true, Passed: false},
				fmt.Sprintf("setup %v failed (exit %d): %s", s, exit, clip(stdout+stderr, 500))
		}
	}
	stdout, stderr, exit := runArgv(ctx, dir, c.Assert)
	passed := exit == c.ExpectExit && stdoutMatches(stdout, c.ExpectStdout)
	return truthalign.ObligationStatus{Executed: true, Passed: passed},
		fmt.Sprintf("assert %v exit=%d(want %d) stdout=%q stderr=%q", c.Assert, exit, c.ExpectExit, clip(stdout, 300), clip(stderr, 300))
}

// proveVerify runs a DECLARED verification command under the allowlisted, sandboxed verifier
// (proofexec.RunTool — secret-scrubbed env, bwrap net+FS deny) and reports whether it satisfies
// the ratified expectation. MustExitZero gates on the exit code; ConfigValidates INDEPENDENTLY
// requires positive evidence (ConfigOKRE matched, ConfigFailRE not) that the tool actually
// parsed the intended config — so a tool that silently fell back to defaults (exit 0, config
// ignored) does NOT pass. A policy/launch failure (disallowed tool, no sandbox, missing binary)
// is Executed=false: the obligation could not be exercised, so the change is not certified.
func proveVerify(ctx context.Context, dir string, v VerifyCommand) (truthalign.ObligationStatus, string) {
	// The "file" pseudo-tool is a static, sandbox-free assertion (no execution) — the safe way to
	// prove a declarative artifact (a Makefile target is defined+wired, a config key present) that
	// can't run under the sandbox.
	if v.Tool == "file" {
		return proveFileAssertion(dir, v)
	}
	if v.CurateGolangci {
		// Vet the generated golangci config (reject plugin/custom-linter keys) into the
		// Orion-controlled copy the command reads via --config. A rejected/invalid config means
		// the obligation cannot be honestly exercised.
		if _, err := toolingcfg.CurateGolangciConfig(filepath.Join(dir, ".golangci.yml"), dir); err != nil {
			return truthalign.ObligationStatus{Executed: false}, "golangci config rejected: " + err.Error()
		}
		// The command MUST read the curated copy, never the CWD-picked generated file.
		if !hasConfigArg(v.Args, toolingcfg.CuratedConfigName) {
			return truthalign.ObligationStatus{Executed: false},
				"golangci verify with curate_golangci must pass --config " + toolingcfg.CuratedConfigName
		}
	}
	stdout, stderr, exit, err := proofexec.RunTool(ctx, dir, v.Tool, v.Args...)
	if err != nil {
		return truthalign.ObligationStatus{Executed: false}, "verifier refused/failed to launch: " + err.Error()
	}
	out := stdout + stderr
	passed := true
	if v.MustExitZero && exit != 0 {
		passed = false
	}
	if v.ConfigValidates {
		if v.ConfigFailRE != "" && reMatch(v.ConfigFailRE, out) {
			passed = false
		}
		if v.ConfigOKRE != "" && !reMatch(v.ConfigOKRE, out) {
			passed = false
		}
	}
	return truthalign.ObligationStatus{Executed: true, Passed: passed},
		fmt.Sprintf("%s %v exit=%d configValidates=%v passed=%v: %s", v.Tool, v.Args, exit, v.ConfigValidates, passed, clip(out, 300))
}

// reMatch reports whether pattern (a regexp) matches s; a malformed pattern never matches.
func reMatch(pattern, s string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// proveFileAssertion is the "file" pseudo-tool: a static, sandbox-free check that a file in the
// worktree (Args[0], a path INSIDE the worktree) matches ConfigOKRE and does NOT match
// ConfigFailRE. It proves a declarative artifact (e.g. a Makefile defines `lint:` invoking
// golangci-lint) WITHOUT executing anything — the honest way to verify tooling that cannot run
// safely under the sandbox (make). A missing file or unmet regex is a fail.
func proveFileAssertion(dir string, v VerifyCommand) (truthalign.ObligationStatus, string) {
	if len(v.Args) == 0 {
		return truthalign.ObligationStatus{Executed: false}, "file assertion: no path given"
	}
	rel := filepath.Clean(v.Args[0])
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return truthalign.ObligationStatus{Executed: false}, "file assertion: path must be inside the worktree"
	}
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return truthalign.ObligationStatus{Executed: true, Passed: false}, "file assertion: " + err.Error()
	}
	content := string(data)
	passed := true
	if v.ConfigOKRE != "" && !reMatch(v.ConfigOKRE, content) {
		passed = false
	}
	if v.ConfigFailRE != "" && reMatch(v.ConfigFailRE, content) {
		passed = false
	}
	return truthalign.ObligationStatus{Executed: true, Passed: passed},
		fmt.Sprintf("file %s assertion passed=%v", rel, passed)
}

// hasConfigArg reports whether args passes --config (or --config=) pointing at the given config
// filename (by base name) — used to enforce that a curated-golangci verify reads the vetted copy.
func hasConfigArg(args []string, name string) bool {
	for i, a := range args {
		if a == "--config" && i+1 < len(args) && filepath.Base(args[i+1]) == name {
			return true
		}
		if v, ok := strings.CutPrefix(a, "--config="); ok && filepath.Base(v) == name {
			return true
		}
	}
	return false
}

// runArgv runs a single argv under safeenv, returning stdout, stderr, and the exit code.
func runArgv(ctx context.Context, dir string, argv []string) (string, string, int) {
	if len(argv) == 0 {
		return "", "", -1
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 -- harness-built toolchain argv (routes through proofexec sandbox)
	cmd.Dir = dir
	cmd.Env = safeenv.Build()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	exit := -1
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	}
	return stdout.String(), stderr.String(), exit
}

// stdoutMatches reports whether got satisfies want: a regexp when want is wrapped in
// /slashes/, otherwise a substring. An empty want matches anything (exit-code-only check).
func stdoutMatches(got, want string) bool {
	if want == "" {
		return true
	}
	if len(want) >= 2 && strings.HasPrefix(want, "/") && strings.HasSuffix(want, "/") {
		re, err := regexp.Compile(want[1 : len(want)-1])
		if err != nil {
			return false
		}
		return re.MatchString(got)
	}
	return strings.Contains(got, want)
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// synthSource renders an in-package Go test file with one call/assert obligation per
// case, wrapped in ORION_OBLIGATION markers. In-package (package <pkgName>) so a Call
// may reach unexported symbols.
func synthSource(pkgName string, cases []Case) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkgName)
	b.WriteString("import (\n\t\"fmt\"\n\t\"reflect\"\n\t\"testing\"\n)\n\n")
	for _, c := range cases {
		fmt.Fprintf(&b, "func Test_orionNB_%s(t *testing.T) {\n", c.ID)
		fmt.Fprintf(&b, "\tfmt.Println(%q)\n", "ORION_OBLIGATION_RUN:"+c.ID)
		fmt.Fprintf(&b, "\tgot := %s\n", c.Synth.Call)
		fmt.Fprintf(&b, "\twant := %s\n", c.Synth.Want)
		b.WriteString("\tif !reflect.DeepEqual(got, want) {\n")
		fmt.Fprintf(&b, "\t\tt.Fatalf(%q, got, want)\n", "obligation "+c.ID+": got %#v, want %#v")
		b.WriteString("\t}\n")
		fmt.Fprintf(&b, "\tfmt.Println(%q)\n", "ORION_OBLIGATION_PASS:"+c.ID)
		b.WriteString("}\n\n")
	}
	return b.String()
}

// packageName reads the package clause from the first non-test .go file in dir.
func packageName(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read package dir %s: %w", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "package "); ok {
				if f := strings.Fields(rest); len(f) > 0 {
					return f[0], nil
				}
			}
		}
	}
	return "", fmt.Errorf("no package clause found in %s", dir)
}

// parseObligations reads ORION_OBLIGATION_RUN/PASS:<id> markers from `go test -v`
// output (mirrors behavioral.parseObligations). RUN → executed; PASS → passed. A case
// whose test panicked or failed to compile prints no markers → never executed.
func parseObligations(output string) map[string]truthalign.ObligationStatus {
	obs := map[string]truthalign.ObligationStatus{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if id, ok := strings.CutPrefix(line, "ORION_OBLIGATION_RUN:"); ok {
			st := obs[id]
			st.Executed = true
			obs[id] = st
		} else if id, ok := strings.CutPrefix(line, "ORION_OBLIGATION_PASS:"); ok {
			st := obs[id]
			st.Executed, st.Passed = true, true
			obs[id] = st
		}
	}
	return obs
}
