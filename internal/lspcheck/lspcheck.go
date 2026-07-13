// Package lspcheck is a cheap, fast pre-proof diagnostics gate (or-ykz.11): it runs the Go
// language server (`gopls check`) over generated code in the coding loop so type/compile
// errors are caught and fed back to the generator BEFORE the expensive behavioral/empirical/
// hazard proof harness runs. It complements — never replaces — the proof harness, which
// compiles the code anyway and remains the right-to-ship authority.
package lspcheck

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/safeenv"
)

// Diagnostic is one gopls diagnostic (a type/compile error or analysis finding).
type Diagnostic struct {
	File string // file path as reported by gopls
	Loc  string // "line:col" or "line:col-endcol"
	Msg  string
}

func (d Diagnostic) String() string {
	if d.File == "" {
		return d.Msg
	}
	return d.File + ":" + d.Loc + ": " + d.Msg
}

// Result is the outcome of a diagnostics pass.
type Result struct {
	Diagnostics []Diagnostic
	Skipped     bool   // gopls absent / unusable → gate skipped (graceful no-op)
	Tool        string // "gopls" when it ran
}

// OK reports whether the code is diagnostic-clean (a skipped gate is OK — the proof harness
// is the backstop).
func (r Result) OK() bool { return len(r.Diagnostics) == 0 }

// Feedback renders the diagnostics as generator feedback for the next refinement attempt.
func (r Result) Feedback() string {
	var b strings.Builder
	b.WriteString("language-server diagnostics — fix these before re-submitting:\n")
	for _, d := range r.Diagnostics {
		b.WriteString("  - ")
		b.WriteString(d.String())
		b.WriteString("\n")
	}
	return b.String()
}

// Diagnose runs `gopls check` over the Go files directly in dir and returns the diagnostics.
// gopls is invoked with a scrubbed environment (safeenv) — the same posture as the proof
// execs — because it statically analyzes UNTRUSTED generated code (no coordinator secrets in
// its env). If gopls is not on PATH, or exits abnormally, the gate is skipped (Skipped=true,
// no error): it degrades to a no-op so a missing/broken gopls never blocks a build.
func Diagnose(ctx context.Context, dir string) (Result, error) {
	bin, err := exec.LookPath("gopls")
	if err != nil {
		return Result{Skipped: true}, nil
	}
	files, err := goFiles(dir)
	if err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		return Result{Tool: "gopls"}, nil
	}
	cmd := exec.CommandContext(ctx, bin, append([]string{"check"}, files...)...) // #nosec G204 -- resolved lsp binary; harness-built file list
	cmd.Dir = dir
	cmd.Env = safeenv.Build()
	out, err := cmd.Output()
	if err != nil {
		// `gopls check` exits 0 even WITH diagnostics (they go to stdout); a non-zero exit is
		// a tool failure (crash, unusable env), not a code defect — skip, leaving the proof
		// harness as the backstop rather than failing the build on a tooling problem.
		if _, ok := err.(*exec.ExitError); ok {
			return Result{Skipped: true, Tool: "gopls"}, nil
		}
		return Result{}, err
	}
	return Result{Diagnostics: parseDiagnostics(string(out)), Tool: "gopls"}, nil
}

// goFiles lists the .go files directly in dir (non-recursive — gopls resolves the package
// from the module), as names relative to dir (cmd.Dir is set to dir).
func goFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			files = append(files, e.Name())
		}
	}
	return files, nil
}

// parseDiagnostics parses `gopls check` output: one diagnostic per line, formatted
// "path:line:col[-endcol]: message". The location segment contains no spaces, so the first
// ": " (colon-space) separates the location from the (possibly colon-bearing) message.
func parseDiagnostics(out string) []Diagnostic {
	var ds []Diagnostic
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		ds = append(ds, parseLine(line))
	}
	return ds
}

func parseLine(line string) Diagnostic {
	loc, msg, ok := strings.Cut(line, ": ")
	if !ok {
		return Diagnostic{Msg: line}
	}
	if j := strings.Index(loc, ".go:"); j >= 0 {
		return Diagnostic{File: loc[:j+3], Loc: loc[j+4:], Msg: msg}
	}
	return Diagnostic{File: loc, Msg: msg}
}
