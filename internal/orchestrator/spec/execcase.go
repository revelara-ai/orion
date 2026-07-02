package spec

import (
	"fmt"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"strings"
)

// CaseKind tags the closed case union (or-v9f.3, Orion-Obligation-Vocabulary-
// Design). The empty kind is the legacy HTTP case — its JSON and content-
// addressed identity stay byte-identical. New kinds are additive; an unknown
// kind is a compile error, never a silent pass (the or-y9d invariant at the
// source).
type CaseKind string

const (
	KindHTTP CaseKind = ""     // legacy HTTP request/response case
	KindExec CaseKind = "exec" // ratified argv against the built artifact ($BIN)
)

// ExecCase is a command-case: seed a scratch dir, run the built artifact with a
// ratified argv, and assert on exit code and output streams. The stimulus and
// every assertion are closed-vocabulary data the proof domain executes
// mechanically — no free-form scripts, no LLM judgment.
type ExecCase struct {
	Seed  []FileSeed `json:"seed,omitempty"` // scratch-dir pre-state
	Steps []ExecStep `json:"steps"`          // slice 1: exactly one run step
}

// FileSeed is one seeded file in the case's scratch working dir.
type FileSeed struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ExecStep is one stimulus against the artifact binary.
type ExecStep struct {
	Op     string            `json:"op,omitempty"` // "" == "run" (the only slice-1 op)
	Argv   []string          `json:"argv"`         // argv[0] MUST be "$BIN"
	Stdin  string            `json:"stdin,omitempty"`
	Env    map[string]string `json:"env,omitempty"` // key-allowlisted
	Expect StepExpect        `json:"expect"`
}

// StepExpect is the observable contract of one step.
type StepExpect struct {
	Exit     *int              `json:"exit,omitempty"`
	WithinMS int               `json:"within_ms,omitempty"` // semantic deadline; hashed; default 15000
	Stdout   []StreamAssertion `json:"stdout,omitempty"`
	Stderr   []StreamAssertion `json:"stderr,omitempty"`
}

// StreamKind is a checkable property of an output stream — semantic kinds, the
// direct heir of BodyAssertion/AssertionKind, so the grill never hand-authors a
// regex a human cannot audit for the common shapes.
type StreamKind string

const (
	StreamExact       StreamKind = "exact"
	StreamContains    StreamKind = "contains"
	StreamRegex       StreamKind = "regex"
	StreamEmpty       StreamKind = "empty"
	StreamRFC3339UTC  StreamKind = "rfc3339_utc"
)

var knownStreamKinds = map[StreamKind]bool{
	StreamExact: true, StreamContains: true, StreamRegex: true,
	StreamEmpty: true, StreamRFC3339UTC: true,
}

// StreamAssertion is one checkable property of stdout or stderr.
type StreamAssertion struct {
	Kind  StreamKind `json:"kind"`
	Value string     `json:"value,omitempty"`
	Key   string     `json:"key,omitempty"`
}

// execEnvAllowlist are the env keys a case may set on the artifact process.
// Reserved prefixes (PATH/HOME/GO/LD_/ORION_) are rejected regardless.
var execEnvAllowlist = map[string]bool{"TZ": true}

const (
	execMaxStdin     = 64 << 10
	execMaxSeedFiles = 32
	execMaxSeedBytes = 256 << 10
	execDefaultMS    = 15000
	execMaxMS        = 60000
)

// validateExecCase is the compile-time battery for exec cases: a case the proof
// domain cannot mechanically run never anchors.
func validateExecCase(c BehavioralCase) error {
	if c.Exec == nil {
		return fmt.Errorf("exec case carries no exec payload")
	}
	if !zeroRequest(c.Request) || !zeroExpect(c.Expect) {
		return fmt.Errorf("exec case must leave the http request/expect zero-valued")
	}
	if len(c.ModesApply) > 0 {
		return fmt.Errorf("exec run cases are mandatorily dual-mode; modes_apply is not accepted")
	}
	if len(c.Exec.Steps) != 1 {
		return fmt.Errorf("exec case must have exactly one step (multi-step lands in a later phase)")
	}
	if len(c.Exec.Seed) > execMaxSeedFiles {
		return fmt.Errorf("exec seed exceeds %d files", execMaxSeedFiles)
	}
	seedBytes := 0
	for _, s := range c.Exec.Seed {
		clean := filepath.Clean(s.Path)
		if clean != s.Path || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return fmt.Errorf("seed path %q must be a clean, relative, contained path", s.Path)
		}
		seedBytes += len(s.Content)
	}
	if seedBytes > execMaxSeedBytes {
		return fmt.Errorf("exec seed exceeds %d bytes total", execMaxSeedBytes)
	}
	st := c.Exec.Steps[0]
	if st.Op != "" && st.Op != "run" {
		return fmt.Errorf("exec op %q is not in the closed op set (slice 1: run)", st.Op)
	}
	if len(st.Argv) == 0 || st.Argv[0] != "$BIN" {
		return fmt.Errorf(`exec argv must be non-empty and start with "$BIN"`)
	}
	for _, a := range st.Argv[1:] {
		if strings.Contains(a, "$") {
			return fmt.Errorf("exec argv %q: only argv[0] may carry a $ token", a)
		}
	}
	if len(st.Stdin) > execMaxStdin {
		return fmt.Errorf("exec stdin exceeds %d bytes", execMaxStdin)
	}
	for k := range st.Env {
		upper := strings.ToUpper(k)
		for _, pfx := range []string{"PATH", "HOME", "GO", "LD_", "ORION_"} {
			if strings.HasPrefix(upper, pfx) {
				return fmt.Errorf("exec env key %q is reserved", k)
			}
		}
		if !execEnvAllowlist[k] {
			return fmt.Errorf("exec env key %q is not on the allowlist", k)
		}
	}
	if st.Expect.Exit == nil && len(st.Expect.Stdout) == 0 && len(st.Expect.Stderr) == 0 {
		return fmt.Errorf("exec expect asserts nothing (need >=1 of exit/stdout/stderr — no vacuous obligations)")
	}
	if st.Expect.WithinMS < 0 || st.Expect.WithinMS > execMaxMS {
		return fmt.Errorf("within_ms %d outside 0..%d", st.Expect.WithinMS, execMaxMS)
	}
	for _, as := range append(append([]StreamAssertion{}, st.Expect.Stdout...), st.Expect.Stderr...) {
		if err := validateStreamAssertion(as); err != nil {
			return err
		}
	}
	return nil
}

func validateStreamAssertion(a StreamAssertion) error {
	if !knownStreamKinds[a.Kind] {
		return fmt.Errorf("unknown stream assertion kind %q", a.Kind)
	}
	switch a.Kind {
	case StreamRegex:
		if _, err := syntax.Parse(a.Value, syntax.Perl); err != nil {
			return fmt.Errorf("stream regex %q does not compile: %v", a.Value, err)
		}
		re, err := regexp.Compile(a.Value)
		if err != nil {
			return fmt.Errorf("stream regex %q does not compile: %v", a.Value, err)
		}
		if re.MatchString("") {
			return fmt.Errorf("stream regex %q matches the empty string (vacuous)", a.Value)
		}
	case StreamContains, StreamExact:
		if a.Value == "" {
			return fmt.Errorf("stream %s assertion needs a non-empty value", a.Kind)
		}
	}
	return nil
}

func zeroRequest(r RequestShape) bool {
	return r.Method == "" && r.Path == "" && len(r.Query) == 0 && len(r.Headers) == 0 && r.Body == ""
}

func zeroExpect(e ExpectShape) bool {
	return e.Status == 0 && e.ContentType == "" && len(e.Assertions) == 0
}
