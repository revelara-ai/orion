package detection

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
)

// Runner is the seam between the Scanner and an actual subprocess
// execution. Tests inject a fake; production callers leave the
// ScannerConfig.Runner field nil and the constructor wires execRunner.
type Runner interface {
	Run(ctx context.Context, binary string, args []string) (stdout, stderr []byte, err error)
}

// Scanner shells out to `rvl scan --local` and adapts its JSON output
// into orion-side Findings.
type Scanner struct {
	runner    Runner
	rvlBinary string
}

// NewScanner constructs a Scanner. Empty cfg.RvlBinary defaults to "rvl"
// (resolved via $PATH at exec time). Empty cfg.Runner defaults to a real
// os/exec-backed runner.
func NewScanner(cfg ScannerConfig) *Scanner {
	if cfg.RvlBinary == "" {
		cfg.RvlBinary = "rvl"
	}
	if cfg.Runner == nil {
		cfg.Runner = execRunner{}
	}
	return &Scanner{runner: cfg.Runner, rvlBinary: cfg.RvlBinary}
}

// Run invokes the subprocess, parses the JSON output, sorts findings
// deterministically, and returns the result.
func (s *Scanner) Run(ctx context.Context, opts ScanOptions) ([]Finding, ScanStats, error) {
	if opts.RepoPath == "" || opts.Service == "" {
		return nil, ScanStats{}, fmt.Errorf("%w: RepoPath=%q Service=%q", ErrInvalidOptions, opts.RepoPath, opts.Service)
	}

	args := []string{
		"scan",
		"--local",
		"--target=" + opts.RepoPath,
		"--service=" + opts.Service,
		"--format=json",
	}

	stdout, stderr, runErr := s.runner.Run(ctx, s.rvlBinary, args)

	// rvl-cli exits non-zero when findings are present (CI gate semantics).
	// Treat stdout as the source of truth: if it parses, the scan succeeded
	// regardless of exit status. Only surface ErrSubprocessFailure when
	// stdout is unparseable AND the subprocess errored (i.e., it really did
	// fail to launch or produce output).
	findings, parseErr := parseRvlOutput(stdout)
	if parseErr != nil {
		if runErr != nil {
			return nil, ScanStats{}, fmt.Errorf("%w: %v (stderr=%s)", ErrSubprocessFailure, runErr, string(stderr))
		}
		return nil, ScanStats{}, fmt.Errorf("%w: %v", ErrParseFailure, parseErr)
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})

	return findings, ScanStats{FindingsTotal: len(findings), RvlBinary: s.rvlBinary}, nil
}

// rvlOutput is the on-the-wire envelope rvl emits.
type rvlOutput struct {
	Findings []rvlFinding `json:"findings"`
}

type rvlFinding struct {
	Slug         string        `json:"slug"`
	Title        string        `json:"title"`
	Category     string        `json:"category"`
	Confidence   string        `json:"confidence"`
	Impact       string        `json:"impact"`
	Likelihood   string        `json:"likelihood"`
	Fingerprint  string        `json:"fingerprint"`
	ControlCodes []string      `json:"control_codes"`
	Narrative    string        `json:"narrative"`
	Evidence     []rvlEvidence `json:"evidence"`
}

type rvlEvidence struct {
	Path       string `json:"path"`
	LineNumber int    `json:"line_number"`
}

func parseRvlOutput(b []byte) ([]Finding, error) {
	var out rvlOutput
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	findings := make([]Finding, 0, len(out.Findings))
	for _, rf := range out.Findings {
		f := Finding{
			Slug:         rf.Slug,
			Title:        rf.Title,
			Category:     rf.Category,
			Confidence:   rf.Confidence,
			Impact:       rf.Impact,
			Likelihood:   rf.Likelihood,
			Fingerprint:  rf.Fingerprint,
			ControlCodes: rf.ControlCodes,
			Narrative:    rf.Narrative,
		}
		// rvl reports one or more evidence sites; we surface the first as
		// the primary location. Future revisions may expose all.
		if len(rf.Evidence) > 0 {
			f.File = rf.Evidence[0].Path
			f.Line = rf.Evidence[0].LineNumber
		}
		findings = append(findings, f)
	}
	return findings, nil
}

// execRunner is the production Runner: a thin wrapper around os/exec.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, binary string, args []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...) //#nosec G204 -- args are constructed from typed ScanOptions; binary is operator-configured
	stdout, err := cmd.Output()
	var stderr []byte
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = exitErr.Stderr
	}
	return stdout, stderr, err
}
