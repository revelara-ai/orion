package spec

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

func intp(n int) *int { return &n }

func execCase(argv []string, expect StepExpect, seed ...FileSeed) BehavioralCase {
	return BehavioralCase{Kind: KindExec, Exec: &ExecCase{Seed: seed, Steps: []ExecStep{{Argv: argv, Expect: expect}}}}
}

// TestLegacyCaseIdentityIsByteStable: the anchor guarantee — the HTTP kind's
// content-addressed identity must never move. This golden pins the exact ID a
// known case hashed to before the union landed; if it changes, every anchored
// spec on every store breaks.
func TestLegacyCaseIdentityIsByteStable(t *testing.T) {
	c := BehavioralCase{
		Request: RequestShape{Method: "GET", Path: "/time"},
		Expect: ExpectShape{Status: 200, ContentType: "application/json",
			Assertions: []BodyAssertion{{Kind: AssertJSONKeyRFC3339, Key: "time"}}},
	}
	c.EnsureID()
	const golden = "c265de60f86f" // caseID as of pre-union main (verified against the un-unioned code)
	if c.ID != golden {
		t.Fatalf("legacy case identity moved: got %s want %s — anchored specs would break", c.ID, golden)
	}
}

// TestExecCaseIdentityIsSurfaceAddressed: exec IDs hash the authored surface
// (kind + modes + payload) — deterministic, and distinct from a same-shaped
// legacy case.
func TestExecCaseIdentityIsSurfaceAddressed(t *testing.T) {
	a := execCase([]string{"$BIN", "--tz=UTC"}, StepExpect{Exit: intp(0)})
	b := execCase([]string{"$BIN", "--tz=UTC"}, StepExpect{Exit: intp(0)})
	a.EnsureID()
	b.EnsureID()
	if a.ID == "" || a.ID != b.ID {
		t.Fatalf("exec identity must be deterministic: %s vs %s", a.ID, b.ID)
	}
	c := execCase([]string{"$BIN", "--tz=Bogus"}, StepExpect{Exit: intp(0)})
	c.EnsureID()
	if c.ID == a.ID {
		t.Fatal("different argv must yield a different identity")
	}
}

// TestExecValidationBattery: a case the proof domain cannot mechanically run
// never anchors.
func TestExecValidationBattery(t *testing.T) {
	valid := execCase([]string{"$BIN", "verify"}, StepExpect{Exit: intp(3),
		Stderr: []StreamAssertion{{Kind: StreamRegex, Value: `(?m)^\S+\.go:\d+`}}},
		FileSeed{Path: "src/a.go", Content: "package a"})

	cases := []struct {
		name   string
		mutate func(*BehavioralCase)
		wantOK bool
		want   string
	}{
		{"valid R10 shape", func(_ *BehavioralCase) {}, true, ""},
		{"missing exec payload", func(c *BehavioralCase) { c.Exec = nil }, false, "no exec payload"},
		{"http fields on exec case", func(c *BehavioralCase) { c.Request.Method = "GET" }, false, "zero-valued"},
		{"modes_apply rejected", func(c *BehavioralCase) { c.ModesApply = []string{"empirical"} }, false, "dual-mode"},
		{"argv not $BIN", func(c *BehavioralCase) { c.Exec.Steps[0].Argv = []string{"rm", "-rf"} }, false, "$BIN"},
		{"dollar token beyond argv0", func(c *BehavioralCase) { c.Exec.Steps[0].Argv = []string{"$BIN", "$HOME"} }, false, "$ token"},
		{"reserved env key", func(c *BehavioralCase) { c.Exec.Steps[0].Env = map[string]string{"GOPATH": "/x"} }, false, "reserved"},
		{"off-allowlist env key", func(c *BehavioralCase) { c.Exec.Steps[0].Env = map[string]string{"SHELL": "/bin/sh"} }, false, "allowlist"},
		{"escaping seed path", func(c *BehavioralCase) { c.Exec.Seed[0].Path = "../evil" }, false, "contained"},
		{"vacuous expectations", func(c *BehavioralCase) { c.Exec.Steps[0].Expect = StepExpect{} }, false, "vacuous"},
		{"empty-matching regex", func(c *BehavioralCase) {
			c.Exec.Steps[0].Expect.Stderr = []StreamAssertion{{Kind: StreamRegex, Value: `.*`}}
		}, false, "empty string"},
		{"unknown op", func(c *BehavioralCase) { c.Exec.Steps[0].Op = "spawn" }, false, "closed op set"},
		{"unknown stream kind", func(c *BehavioralCase) {
			c.Exec.Steps[0].Expect.Stderr = []StreamAssertion{{Kind: "sniff", Value: "x"}}
		}, false, "unknown stream assertion"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			// deep-ish copy the parts the mutations touch
			ex := *valid.Exec
			ex.Seed = append([]FileSeed{}, valid.Exec.Seed...)
			ex.Steps = append([]ExecStep{}, valid.Exec.Steps...)
			c.Exec = &ex
			tc.mutate(&c)
			err := ValidateRequirement(Requirement{Source: completeness.DimFunctional, Text: "t", Cases: []BehavioralCase{c}})
			if tc.wantOK && err != nil {
				t.Fatalf("valid case rejected: %v", err)
			}
			if !tc.wantOK {
				if err == nil {
					t.Fatal("invalid case anchored")
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error should mention %q, got: %v", tc.want, err)
				}
			}
		})
	}
}

// TestExecContradictions: one stimulus demanding two outcomes refuses to anchor;
// different stimuli compose.
func TestExecContradictions(t *testing.T) {
	seed := FileSeed{Path: "src/a.go", Content: "package a"}
	conflictA := execCase([]string{"$BIN", "verify"}, StepExpect{Exit: intp(0)}, seed)
	conflictB := execCase([]string{"$BIN", "verify"}, StepExpect{Exit: intp(3)}, seed)
	if cs := FindContradictions([]BehavioralCase{conflictA, conflictB}); len(cs) != 1 {
		t.Fatalf("same stimulus, different exit must conflict, got %+v", cs)
	}

	okA := execCase([]string{"$BIN", "verify"}, StepExpect{Exit: intp(0)})
	okB := execCase([]string{"$BIN", "verify"}, StepExpect{Exit: intp(3)}, seed) // different seed = different stimulus
	if cs := FindContradictions([]BehavioralCase{okA, okB}); len(cs) != 0 {
		t.Fatalf("different stimuli must compose, got %+v", cs)
	}

	exactVsEmpty := []BehavioralCase{
		execCase([]string{"$BIN"}, StepExpect{Stdout: []StreamAssertion{{Kind: StreamExact, Value: "ok"}}}),
		execCase([]string{"$BIN"}, StepExpect{Stdout: []StreamAssertion{{Kind: StreamEmpty}}}),
	}
	if cs := FindContradictions(exactVsEmpty); len(cs) != 1 {
		t.Fatalf("exact vs empty on one stream must conflict, got %+v", cs)
	}

	// http and exec never cross-group even with superficially similar shapes.
	httpCase := BehavioralCase{Request: RequestShape{Method: "GET", Path: "/verify"}, Expect: ExpectShape{Status: 200, ContentType: "application/json"}}
	if cs := FindContradictions([]BehavioralCase{httpCase, okA}); len(cs) != 0 {
		t.Fatalf("kinds must not cross-group, got %+v", cs)
	}
}
