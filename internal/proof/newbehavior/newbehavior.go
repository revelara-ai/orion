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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// Case is one ratified new-behavior obligation.
type Case struct {
	ID       string // content-addressed; derived from the payload when empty
	Modality string // "synth_test" (slice 1a) | "command" (slice 1b)
	Synth    *SynthTest
}

const synthTestFile = "orion_newbehavior_test.go"

// withID returns the case with a content-addressed ID when one is not set.
func (c Case) withID() Case {
	if c.ID != "" {
		return c
	}
	h := sha256.Sum256([]byte(c.Modality + "\x00" + c.Synth.Pkg + "\x00" + c.Synth.Call + "\x00" + c.Synth.Want))
	c.ID = hex.EncodeToString(h[:])[:12]
	return c
}

// ProveNewBehavior proves the ratified cases against the changed artifact in
// artifactDir and returns a "new_behavior" ModeResult (per-case obligations + verdict).
// The verdict is Accept (Pass) only if every case Executed AND Passed; an empty/never-run
// set is Inconclusive (a coverage hole, not a silent pass).
func ProveNewBehavior(ctx context.Context, artifactDir string, cases []Case) (truthalign.ModeResult, error) {
	mr := truthalign.ModeResult{Mode: "new_behavior", Obligations: map[string]truthalign.ObligationStatus{}}

	// Group synth_test cases by package (one synthesized file + one `go test` per pkg).
	byPkg := map[string][]Case{}
	var ids []string
	for _, c := range cases {
		if c.Modality != "synth_test" || c.Synth == nil {
			continue // command modality is slice 1b
		}
		c = c.withID()
		byPkg[c.Synth.Pkg] = append(byPkg[c.Synth.Pkg], c)
		ids = append(ids, c.ID)
	}

	var out strings.Builder
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
