package detection_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/revelara-ai/orion/internal/detection"
)

// readFixture loads the canned rvl-cli JSON output fixture.
func readFixture(t *testing.T) []byte {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	p := filepath.Join(filepath.Dir(here), "testdata", "rvl_sample.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read fixture %s: %v", p, err)
	}
	return data
}

// fakeRunner returns the canned bytes regardless of args; records the args
// for assertion.
type fakeRunner struct {
	out      []byte
	stderr   string
	exitErr  error
	gotArgs  []string
	callerCt int
}

func (f *fakeRunner) Run(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
	f.callerCt++
	f.gotArgs = args
	return f.out, []byte(f.stderr), f.exitErr
}

func TestScanner_Run_ParsesFindings(t *testing.T) {
	r := &fakeRunner{out: readFixture(t)}
	s := detection.NewScanner(detection.ScannerConfig{Runner: r, RvlBinary: "rvl"})

	findings, stats, err := s.Run(context.Background(), detection.ScanOptions{
		RepoPath: "/tmp/repo",
		Service:  "checkoutservice",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(findings))
	}
	if stats.FindingsTotal != 2 {
		t.Errorf("stats.FindingsTotal=%d, want 2", stats.FindingsTotal)
	}

	// Findings should be sorted deterministically (file ascending, then line)
	for i := 1; i < len(findings); i++ {
		prev, cur := findings[i-1], findings[i]
		if prev.File > cur.File || (prev.File == cur.File && prev.Line > cur.Line) {
			t.Errorf("findings not deterministically sorted: %v before %v", prev, cur)
		}
	}

	// Spot-check field plumbing on the first (sorted) finding.
	first := findings[0]
	if first.File == "" {
		t.Error("Finding.File is empty")
	}
	if first.Line <= 0 {
		t.Errorf("Finding.Line=%d, want > 0", first.Line)
	}
	if first.Slug == "" {
		t.Error("Finding.Slug is empty")
	}
	if first.Fingerprint == "" {
		t.Error("Finding.Fingerprint is empty")
	}
	if first.Category == "" {
		t.Error("Finding.Category is empty")
	}
	if len(first.ControlCodes) == 0 {
		t.Error("Finding.ControlCodes is empty")
	}
	if first.Confidence == "" {
		t.Error("Finding.Confidence is empty")
	}
}

func TestScanner_Run_PassesCorrectArgs(t *testing.T) {
	r := &fakeRunner{out: readFixture(t)}
	s := detection.NewScanner(detection.ScannerConfig{Runner: r, RvlBinary: "rvl"})

	_, _, err := s.Run(context.Background(), detection.ScanOptions{
		RepoPath: "/tmp/repo",
		Service:  "frontend",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"scan", "--local", "--target=/tmp/repo", "--service=frontend", "--format=json"}
	if !equalStringSlice(r.gotArgs, want) {
		t.Errorf("args mismatch:\n got:  %v\n want: %v", r.gotArgs, want)
	}
}

func TestScanner_Run_RejectsMalformedJSON(t *testing.T) {
	r := &fakeRunner{out: []byte("not json {")}
	s := detection.NewScanner(detection.ScannerConfig{Runner: r, RvlBinary: "rvl"})

	_, _, err := s.Run(context.Background(), detection.ScanOptions{RepoPath: "/tmp", Service: "x"})
	if err == nil {
		t.Fatal("want error on malformed JSON, got nil")
	}
	if !errors.Is(err, detection.ErrParseFailure) {
		t.Errorf("want errors.Is(err, ErrParseFailure), got %v", err)
	}
}

func TestScanner_Run_PropagatesSubprocessError(t *testing.T) {
	r := &fakeRunner{exitErr: errors.New("rvl: not found"), stderr: "command not found: rvl"}
	s := detection.NewScanner(detection.ScannerConfig{Runner: r, RvlBinary: "rvl"})

	_, _, err := s.Run(context.Background(), detection.ScanOptions{RepoPath: "/tmp", Service: "x"})
	if err == nil {
		t.Fatal("want error when subprocess fails, got nil")
	}
	if !errors.Is(err, detection.ErrSubprocessFailure) {
		t.Errorf("want errors.Is(err, ErrSubprocessFailure), got %v", err)
	}
}

func TestScanner_Run_RequiresRepoAndService(t *testing.T) {
	s := detection.NewScanner(detection.ScannerConfig{Runner: &fakeRunner{}, RvlBinary: "rvl"})

	cases := []struct {
		name string
		opts detection.ScanOptions
	}{
		{"empty repo", detection.ScanOptions{Service: "x"}},
		{"empty service", detection.ScanOptions{RepoPath: "/tmp"}},
		{"both empty", detection.ScanOptions{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := s.Run(context.Background(), tc.opts)
			if err == nil {
				t.Fatalf("want validation error for %v, got nil", tc.opts)
			}
			if !errors.Is(err, detection.ErrInvalidOptions) {
				t.Errorf("want ErrInvalidOptions, got %v", err)
			}
		})
	}
}

func TestScanner_DefaultRunner_UsesRvlBinary(t *testing.T) {
	// When no Runner is supplied, NewScanner falls back to a real exec
	// runner. Just verify the constructor doesn't panic and that the
	// returned Scanner is non-nil; live invocation lives in integration_test.go.
	s := detection.NewScanner(detection.ScannerConfig{RvlBinary: "rvl"})
	if s == nil {
		t.Fatal("NewScanner returned nil")
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
