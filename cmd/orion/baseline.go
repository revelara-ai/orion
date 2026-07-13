package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/revelara-ai/orion/internal/brownfield"
)

// cmdBaseline captures a target repo's regression baseline: it detects the repo's
// toolchain and runs its existing tests, reporting green/red. This is the first
// brownfield primitive — the invariant a proven change must preserve. It shows a
// live spinner while the (minutes-long) suite runs and a green/red summary, with
// only the failing packages expanded — never the whole `go test` dump (or-rbc).
// Usage:
//
//	orion baseline [dir]   (dir defaults to .)
func cmdBaseline(args []string) int {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	abs, err := os.Getwd()
	if err == nil && dir == "." {
		dir = abs
	}

	// Step-2 fork: greenfield (create new structure) vs brownfield (integrate with
	// existing code). This decides the create-vs-edit path for everything downstream.
	prof := brownfield.Classify(dir)
	fmt.Printf("repo: %s\n  mode: %s", dir, prof.Mode)
	if len(prof.Languages) > 0 {
		fmt.Printf(" (%s)", strings.Join(prof.Languages, ", "))
	}
	fmt.Printf("\n  git: %v (commits: %v) · source files: %d · tests: %v\n\n", prof.HasGit, prof.HasCommits, prof.SourceFiles, prof.HasTests)

	res, err := runBaseline(context.Background(), dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion baseline:", err)
		return 1
	}
	if !res.Detected {
		fmt.Printf("baseline: no regression baseline for %s — %s\n", dir, res.Skipped)
		return 0 // not an error: the repo simply offers no test baseline
	}
	fmt.Printf("baseline: %s · toolchain %s · %s\n", dir, res.Toolchain, res.Command)
	fmt.Print(formatBaselineReport(res))
	if !res.Passed {
		return 1 // a red baseline means a change can't yet be regression-proven
	}
	return 0
}

// runBaseline runs the suite with a live spinner (on a TTY): `go test` streams
// one line per package, which the spinner turns into a running "N packages, M
// failing" tally so a silent 10-minute gate is visibly alive, not hung.
func runBaseline(ctx context.Context, dir string) (brownfield.TestResult, error) {
	st := &baselineProgress{}
	stop, done := make(chan struct{}), make(chan struct{})
	if term.IsTerminal(int(os.Stderr.Fd())) {
		go st.spin(os.Stderr, stop, done)
	} else {
		close(done)
	}
	res, err := brownfield.BaselineProgress(ctx, dir, st.sink())
	close(stop)
	<-done // the spinner clears its line before the summary prints
	return res, err
}

// formatBaselineReport renders the green/red summary. Green is a one-line tally;
// red is the tally plus ONLY the failing packages' output — never the whole run,
// which is the raw dump this command exists to replace.
func formatBaselineReport(res brownfield.TestResult) string {
	sum := brownfield.Summarize(res.Output)
	var b strings.Builder
	if res.Passed {
		fmt.Fprintf(&b, "  ✓ GREEN — %d packages passed", len(sum.Green))
		if len(sum.NoTests) > 0 {
			fmt.Fprintf(&b, " · %d with no tests", len(sum.NoTests))
		}
		b.WriteByte('\n')
		return b.String()
	}
	fmt.Fprintf(&b, "  ✗ RED — %d passed / %d failed", len(sum.Green), len(sum.Failed))
	if len(sum.NoTests) > 0 {
		fmt.Fprintf(&b, " · %d with no tests", len(sum.NoTests))
	}
	b.WriteByte('\n')
	for _, pkg := range sum.Failed {
		fmt.Fprintf(&b, "\n── %s ──\n%s\n", pkg, sum.Blocks[pkg])
	}
	return b.String()
}

// baselineProgress is the live tally behind the spinner: it counts package
// completions and, separately, failures, from the brownfield progress stream.
type baselineProgress struct {
	mu      sync.Mutex
	done    int
	failing int
	last    string
}

// sink returns the brownfield.Progress that feeds the tally. Each event is one
// package completion, "<pkg> <verdict> (n done)"; a FAIL verdict bumps failing.
func (s *baselineProgress) sink() brownfield.Progress {
	return func(_, detail string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.done++
		if strings.Contains(detail, " FAIL ") {
			s.failing++
		}
		s.last = detail
	}
}

// status renders the one-line spinner status for the given frame rune.
func (s *baselineProgress) status(frame rune) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := fmt.Sprintf("%c running tests — %d packages", frame, s.done)
	if s.failing > 0 {
		msg += fmt.Sprintf(", %d failing", s.failing)
	}
	return msg
}

// spin animates the status line on w until stop is closed, then clears the line
// and closes done. TTY-only; the caller gates on term.IsTerminal.
func (s *baselineProgress) spin(w *os.File, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	tk := time.NewTicker(120 * time.Millisecond)
	defer tk.Stop()
	for i := 0; ; i++ {
		select {
		case <-stop:
			fmt.Fprint(w, "\r\033[K") // erase the spinner line
			return
		case <-tk.C:
			fmt.Fprintf(w, "\r\033[K%s", s.status(frames[i%len(frames)]))
		}
	}
}
