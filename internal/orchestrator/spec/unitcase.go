package spec

import (
	"fmt"
	"go/parser"
	"path/filepath"
	"strings"
)

// Phase 2 case kinds (or-v9f.23, Orion-Obligation-Vocabulary-Design §3/§7):
// unit — exported/in-package call-and-assert obligations (the library surface);
// file — static assertions on the ARTIFACT TREE (runbooks, configs, emitted docs).
const (
	KindUnit CaseKind = "unit"
	KindFile CaseKind = "file"
)

// ModesRationale is the closed enum justifying a narrowed ModesApply. Slice:
// cross_process_persistence (unit Restart steps are empirical-only — an
// in-process test cannot cross a process boundary).
const RationaleCrossProcess = "cross_process_persistence"

var knownModesRationales = map[string]bool{RationaleCrossProcess: true}

// UnitCase proves package-level behavior: sequential call-and-assert steps
// evaluated IN the artifact's package scope. A Restart step marks a process
// boundary — the empirical driver re-execs there while on-disk state persists
// (genuine restart, R9-class persistence proofs).
type UnitCase struct {
	Pkg   string     `json:"pkg,omitempty"` // package dir relative to the module root; "" = root package
	Steps []UnitStep `json:"steps"`
}

// UnitStep is one call-and-assert. Exactly one of Want / WantErrRE is set:
// Want compares the call's value (reflect.DeepEqual posture, newbehavior's
// proven semantics); WantErrRE requires the call to yield an error matching it.
type UnitStep struct {
	Call      string `json:"call"`                  // Go expression in package scope
	Want      string `json:"want,omitempty"`        // expected value as a Go literal
	WantErrRE string `json:"want_err_re,omitempty"` // OR: error text must match
	Restart   bool   `json:"restart,omitempty"`     // process boundary BEFORE this step (empirical channel)
}

// FileCase proves the artifact TREE carries what it must: paths exist/are
// absent, and file contents satisfy stream assertions. Identical deterministic
// check in both proof channels.
type FileCase struct {
	Assertions []FileAssertion `json:"assertions"`
}

// FileKind is the closed file-assertion vocabulary.
type FileKind string

const (
	FileExists   FileKind = "exists"
	FileAbsent   FileKind = "absent"
	FileContains FileKind = "contains"
	FileRegex    FileKind = "regex"
)

var knownFileKinds = map[FileKind]bool{FileExists: true, FileAbsent: true, FileContains: true, FileRegex: true}

// FileAssertion is one checkable property of one artifact-tree path.
type FileAssertion struct {
	Path  string   `json:"path"`
	Kind  FileKind `json:"kind"`
	Value string   `json:"value,omitempty"`
}

const (
	unitMaxSteps      = 16
	fileMaxAssertions = 32
)

// validateUnitCase: the compile-time battery for unit cases.
func validateUnitCase(c BehavioralCase) error {
	if c.Unit == nil {
		return fmt.Errorf("unit case carries no unit payload")
	}
	if !zeroRequest(c.Request) || !zeroExpect(c.Expect) || c.Exec != nil {
		return fmt.Errorf("unit case must leave other kind payloads zero-valued")
	}
	if c.Unit.Pkg != "" {
		clean := filepath.Clean(c.Unit.Pkg)
		if clean != c.Unit.Pkg || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return fmt.Errorf("unit pkg %q must be a clean, relative, contained path", c.Unit.Pkg)
		}
	}
	if n := len(c.Unit.Steps); n == 0 || n > unitMaxSteps {
		return fmt.Errorf("unit case needs 1..%d steps, got %d", unitMaxSteps, n)
	}
	hasRestart := false
	for i, st := range c.Unit.Steps {
		if strings.TrimSpace(st.Call) == "" {
			return fmt.Errorf("unit step %d: empty call", i)
		}
		if (st.Want == "") == (st.WantErrRE == "") {
			return fmt.Errorf("unit step %d: exactly one of want / want_err_re must be set", i)
		}
		if st.WantErrRE != "" {
			if err := validateStreamAssertion(StreamAssertion{Kind: StreamRegex, Value: st.WantErrRE}); err != nil {
				return fmt.Errorf("unit step %d: %w", i, err)
			}
		}
		// The call must at least parse as a Go expression — a case the corpus
		// cannot compile from must not anchor (deeper resolution is the corpus
		// compile + the fast-tier diagnostic's job).
		if _, err := parser.ParseExpr(st.Call); err != nil {
			return fmt.Errorf("unit step %d: call does not parse as a Go expression: %v", i, err)
		}
		if st.Want != "" {
			if _, err := parser.ParseExpr(st.Want); err != nil {
				return fmt.Errorf("unit step %d: want does not parse as a Go literal/expression: %v", i, err)
			}
		}
		if st.Restart {
			if i == 0 {
				return fmt.Errorf("unit step 0 cannot restart (nothing before the boundary)")
			}
			hasRestart = true
		}
	}
	if hasRestart {
		if len(c.ModesApply) != 1 || c.ModesApply[0] != "empirical" || c.ModesRationale != RationaleCrossProcess {
			return fmt.Errorf(`unit cases with restart steps cross a process boundary an in-process test cannot: set modes_apply ["empirical"] with modes_rationale %q`, RationaleCrossProcess)
		}
	} else if len(c.ModesApply) > 0 {
		return fmt.Errorf("unit cases without restart steps are mandatorily dual-mode; modes_apply is not accepted")
	}
	return nil
}

// validateFileCase: the compile-time battery for file cases.
func validateFileCase(c BehavioralCase) error {
	if c.File == nil {
		return fmt.Errorf("file case carries no file payload")
	}
	if !zeroRequest(c.Request) || !zeroExpect(c.Expect) || c.Exec != nil || c.Unit != nil {
		return fmt.Errorf("file case must leave other kind payloads zero-valued")
	}
	if len(c.ModesApply) > 0 {
		return fmt.Errorf("file cases run the identical check in both modes; modes_apply is not accepted")
	}
	if n := len(c.File.Assertions); n == 0 || n > fileMaxAssertions {
		return fmt.Errorf("file case needs 1..%d assertions, got %d", fileMaxAssertions, n)
	}
	for i, a := range c.File.Assertions {
		clean := filepath.Clean(a.Path)
		if a.Path == "" || clean != a.Path || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return fmt.Errorf("file assertion %d: path %q must be a clean, relative, contained path", i, a.Path)
		}
		if !knownFileKinds[a.Kind] {
			return fmt.Errorf("file assertion %d: unknown kind %q", i, a.Kind)
		}
		switch a.Kind {
		case FileContains:
			if a.Value == "" {
				return fmt.Errorf("file assertion %d: contains needs a non-empty value", i)
			}
		case FileRegex:
			if err := validateStreamAssertion(StreamAssertion{Kind: StreamRegex, Value: a.Value}); err != nil {
				return fmt.Errorf("file assertion %d: %w", i, err)
			}
		case FileExists, FileAbsent:
			if a.Value != "" {
				return fmt.Errorf("file assertion %d: %s takes no value", i, a.Kind)
			}
		}
	}
	return nil
}
