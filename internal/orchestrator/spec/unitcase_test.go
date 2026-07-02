package spec

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

func unitCase(pkg string, steps ...UnitStep) BehavioralCase {
	return BehavioralCase{Kind: KindUnit, Unit: &UnitCase{Pkg: pkg, Steps: steps}}
}

func fileCase(as ...FileAssertion) BehavioralCase {
	return BehavioralCase{Kind: KindFile, File: &FileCase{Assertions: as}}
}

func validate1(c BehavioralCase) error {
	return ValidateRequirement(Requirement{Source: completeness.DimFunctional, Text: "t", Cases: []BehavioralCase{c}})
}

// TestUnitValidationBattery: unit cases anchor only when mechanically provable.
func TestUnitValidationBattery(t *testing.T) {
	ok := unitCase("", UnitStep{Call: `ParseConfig("missing.yaml")`, WantErrRE: `missing.*field`})
	if err := validate1(ok); err != nil {
		t.Fatalf("valid unit case rejected: %v", err)
	}
	cases := []struct {
		name string
		c    BehavioralCase
		want string
	}{
		{"both want and err", unitCase("", UnitStep{Call: "F()", Want: "1", WantErrRE: "x"}), "exactly one"},
		{"neither want nor err", unitCase("", UnitStep{Call: "F()"}), "exactly one"},
		{"unparseable call", unitCase("", UnitStep{Call: "func {", Want: "1"}), "does not parse"},
		{"unparseable want", unitCase("", UnitStep{Call: "F()", Want: "struct {"}), "does not parse"},
		{"escaping pkg", unitCase("../evil", UnitStep{Call: "F()", Want: "1"}), "contained"},
		{"restart at step 0", BehavioralCase{Kind: KindUnit, ModesApply: []string{"empirical"}, ModesRationale: RationaleCrossProcess,
			Unit: &UnitCase{Steps: []UnitStep{{Call: "F()", Want: "1", Restart: true}}}}, "step 0 cannot restart"},
		{"restart without narrowing", unitCase("", UnitStep{Call: "Put()", Want: "nil"}, UnitStep{Call: "Get()", Want: "1", Restart: true}), "modes_apply"},
		{"narrowing without restart", BehavioralCase{Kind: KindUnit, ModesApply: []string{"empirical"}, ModesRationale: RationaleCrossProcess,
			Unit: &UnitCase{Steps: []UnitStep{{Call: "F()", Want: "1"}}}}, "modes_apply is not accepted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validate1(tc.c)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error mentioning %q, got: %v", tc.want, err)
			}
		})
	}
	// The R9 shape: restart + narrowing + rationale anchors.
	r9 := BehavioralCase{Kind: KindUnit, ModesApply: []string{"empirical"}, ModesRationale: RationaleCrossProcess,
		Unit: &UnitCase{Steps: []UnitStep{
			{Call: `Put("k", "v")`, Want: "nil"},
			{Call: `Get("k")`, Want: `"v"`, Restart: true},
		}}}
	if err := validate1(r9); err != nil {
		t.Fatalf("the R9 persistence shape must anchor: %v", err)
	}
}

// TestFileValidationBattery: file cases anchor only when mechanically checkable.
func TestFileValidationBattery(t *testing.T) {
	if err := validate1(fileCase(
		FileAssertion{Path: "runbook.md", Kind: FileContains, Value: "incident_response"},
		FileAssertion{Path: "secrets.txt", Kind: FileAbsent},
	)); err != nil {
		t.Fatalf("valid file case rejected: %v", err)
	}
	bad := []struct {
		name string
		a    FileAssertion
		want string
	}{
		{"escaping path", FileAssertion{Path: "../x", Kind: FileExists}, "contained"},
		{"unknown kind", FileAssertion{Path: "a", Kind: "sniffs"}, "unknown kind"},
		{"contains without value", FileAssertion{Path: "a", Kind: FileContains}, "non-empty value"},
		{"exists with value", FileAssertion{Path: "a", Kind: FileExists, Value: "x"}, "takes no value"},
		{"vacuous regex", FileAssertion{Path: "a", Kind: FileRegex, Value: ".*"}, "empty string"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if err := validate1(fileCase(tc.a)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error mentioning %q, got: %v", tc.want, err)
			}
		})
	}
}

// TestUnitFileIdentityAndContradictions: surface-addressed IDs; decidable conflicts.
func TestUnitFileIdentityAndContradictions(t *testing.T) {
	a := unitCase("", UnitStep{Call: "F()", Want: "1"})
	b := unitCase("", UnitStep{Call: "F()", Want: "2"})
	a.EnsureID()
	b.EnsureID()
	if a.ID == b.ID {
		t.Fatal("different wants must yield different identities")
	}
	if cs := FindContradictions([]BehavioralCase{a, b}); len(cs) != 1 {
		t.Fatalf("same call different want must conflict, got %+v", cs)
	}
	errCase := unitCase("", UnitStep{Call: "F()", WantErrRE: "boom"})
	errCase.EnsureID()
	if cs := FindContradictions([]BehavioralCase{a, errCase}); len(cs) != 1 {
		t.Fatalf("value-vs-error for one call must conflict, got %+v", cs)
	}

	fx := fileCase(FileAssertion{Path: "runbook.md", Kind: FileContains, Value: "x"})
	fy := fileCase(FileAssertion{Path: "runbook.md", Kind: FileAbsent})
	fx.EnsureID()
	fy.EnsureID()
	if cs := FindContradictions([]BehavioralCase{fx, fy}); len(cs) != 1 {
		t.Fatalf("content-vs-absent on one path must conflict, got %+v", cs)
	}
	fz := fileCase(FileAssertion{Path: "other.md", Kind: FileAbsent})
	fz.EnsureID()
	if cs := FindContradictions([]BehavioralCase{fx, fz}); len(cs) != 0 {
		t.Fatalf("different paths compose, got %+v", cs)
	}
}
