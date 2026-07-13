package harnessconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCanaryPickDeterministicAndFractional: same key same verdict; 0 and 1
// are absolute; a middle fraction splits a key population.
func TestCanaryPickDeterministicAndFractional(t *testing.T) {
	if canaryPick("acme/svc", 0) {
		t.Fatal("fraction 0 must never pick")
	}
	if !canaryPick("acme/svc", 1) {
		t.Fatal("fraction 1 must always pick")
	}
	first := canaryPick("acme/svc", 0.5)
	for i := 0; i < 10; i++ {
		if canaryPick("acme/svc", 0.5) != first {
			t.Fatal("the pick must be deterministic per key")
		}
	}
	in := 0
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t"} {
		if canaryPick(k, 0.5) {
			in++
		}
	}
	if in == 0 || in == 20 {
		t.Fatalf("a 0.5 fraction must split the population, got %d/20", in)
	}
}

// TestCanaryRolloutAndRollback (or-mvr.6 acceptance): a prompt change ships
// behind the versioned manifest with a canary fraction — cohort sites read
// the candidate, others read stable — and ONE command (Rollback) returns
// every site to stable, no recompile anywhere.
func TestCanaryRolloutAndRollback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORION_HARNESS_DIR", dir)
	stable := "STABLE PREAMBLE {{.Module}}"
	cand := "CANDIDATE PREAMBLE {{.Module}}"
	if err := os.WriteFile(filepath.Join(dir, "generation_preamble.tmpl"), []byte(stable), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := StartCanary(2, 1.0); err != nil { // fraction 1: every site is in the cohort
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "candidate", "generation_preamble.tmpl"), []byte(cand), 0o644); err != nil {
		t.Fatal(err)
	}

	out, ok := GenerationPreamble(PreambleData{Module: "acme/svc"})
	if !ok || !strings.Contains(out, "CANDIDATE PREAMBLE acme/svc") {
		t.Fatalf("a cohort site must read the candidate: ok=%v %q", ok, out)
	}
	if !strings.Contains(CanaryStatus(), "canary v2 at fraction 1") {
		t.Fatalf("status must name version+fraction: %s", CanaryStatus())
	}

	// Fraction 0: canary active but nobody in the cohort → stable.
	if err := StartCanary(2, 0.0); err != nil {
		t.Fatal(err)
	}
	if out, _ := GenerationPreamble(PreambleData{Module: "acme/svc"}); !strings.Contains(out, "STABLE PREAMBLE") {
		t.Fatalf("fraction 0 must read stable: %q", out)
	}

	// ONE-COMMAND ROLLBACK: back to stable for everyone, candidate preserved
	// on disk for the post-mortem.
	if err := StartCanary(2, 1.0); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(); err != nil {
		t.Fatal(err)
	}
	out, _ = GenerationPreamble(PreambleData{Module: "acme/svc"})
	if !strings.Contains(out, "STABLE PREAMBLE acme/svc") {
		t.Fatalf("after rollback every site reads stable: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "candidate", "generation_preamble.tmpl")); err != nil {
		t.Fatal("rollback must preserve the candidate for the post-mortem")
	}
	if err := Rollback(); err != nil {
		t.Fatal("rollback must be idempotent")
	}

	// A canary WITHOUT a candidate file canaries nothing (never a 404 deploy).
	if err := StartCanary(3, 1.0); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "candidate", "generation_preamble.tmpl")); err != nil {
		t.Fatal(err)
	}
	if out, _ := GenerationPreamble(PreambleData{Module: "acme/svc"}); !strings.Contains(out, "STABLE PREAMBLE") {
		t.Fatalf("missing candidate file must fall back to stable: %q", out)
	}
}

// TestCanaryPromote: promote graduates the candidate over stable and ends the
// canary — one command, no recompile.
func TestCanaryPromote(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORION_HARNESS_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "rules.md"), []byte("old rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := StartCanary(4, 0.5); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "candidate", "rules.md"), []byte("new rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Promote(); err != nil {
		t.Fatal(err)
	}
	if got := Rules("any-site"); got != "new rule" {
		t.Fatalf("promotion must graduate the candidate to stable, got %q", got)
	}
	if strings.Contains(CanaryStatus(), "canary v") {
		t.Fatalf("promotion must end the canary: %s", CanaryStatus())
	}
}

// TestCanaryManifestValidation: bad fraction/version refuse at write AND are
// named by Validate when hand-edited.
func TestCanaryManifestValidation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORION_HARNESS_DIR", dir)
	if err := StartCanary(1, 1.5); err == nil {
		t.Fatal("fraction >1 must refuse")
	}
	if err := StartCanary(0, 0.5); err == nil {
		t.Fatal("version 0 must refuse")
	}
	if err := os.WriteFile(filepath.Join(dir, "canary.yaml"), []byte("version: 1\nfraction: 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if errs := Validate(); len(errs) != 1 || !strings.Contains(errs[0].Error(), "canary.yaml") {
		t.Fatalf("Validate must name the broken manifest: %v", errs)
	}
	// And a broken manifest deploys NOTHING (fail-closed to stable).
	if err := os.WriteFile(filepath.Join(dir, "rules.md"), []byte("stable rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "candidate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "candidate", "rules.md"), []byte("candidate rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Rules("any"); got != "stable rule" {
		t.Fatalf("an invalid manifest must fail closed to stable, got %q", got)
	}
}
