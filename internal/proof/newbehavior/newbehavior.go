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

	"github.com/revelara-ai/orion/internal/proof/safeenv"
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

// Case is one ratified new-behavior obligation.
type Case struct {
	ID       string // content-addressed; derived from the payload when empty
	Modality string // "synth_test" (slice 1a) | "command" (slice 1b)
	Synth    *SynthTest
	Command  *Command
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
	cmd := exec.CommandContext(ctx, "go", "test", "-v", "-run", "Test_orionNB_", "./"+filepath.ToSlash(pkg))
	cmd.Dir = moduleRoot
	cmd.Env = safeenv.Build()
	out, err := cmd.CombinedOutput()
	return string(out), err
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

// runArgv runs a single argv under safeenv, returning stdout, stderr, and the exit code.
func runArgv(ctx context.Context, dir string, argv []string) (string, string, int) {
	if len(argv) == 0 {
		return "", "", -1
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
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
