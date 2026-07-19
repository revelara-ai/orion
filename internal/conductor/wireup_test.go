package conductor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof"
)

func wuWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSystemWireupGateDetectsOrphan (or-tcs.3): a package built but reachable from no main is an
// orphan — the assembled system is REJECTED even though every per-task proof passed.
func TestSystemWireupGateDetectsOrphan(t *testing.T) {
	dir := t.TempDir()
	wuWrite(t, dir, "go.mod", "module svc\n\ngo 1.23\n")
	wuWrite(t, dir, "cmd/app/main.go", "package main\n\nimport \"svc/internal/handler\"\n\nfunc main() { handler.H() }\n")
	wuWrite(t, dir, "internal/handler/h.go", "package handler\n\nfunc H() {}\n") // wired (main imports it)
	wuWrite(t, dir, "internal/orphan/o.go", "package orphan\n\nfunc O() {}\n")   // ORPHAN — nobody imports it

	verdict, orphans := systemWireupGate(dir, "go")
	if verdict != WireupOrphaned {
		t.Error("an unwired package must yield WireupOrphaned")
	}
	if len(orphans) != 1 || !strings.Contains(orphans[0], "orphan") {
		t.Errorf("expected the orphan package to be flagged, got %v", orphans)
	}
}

// TestSystemWireupGatePassesWhenAllWired: a fully-wired tree (every package reachable from main) passes.
func TestSystemWireupGatePassesWhenAllWired(t *testing.T) {
	dir := t.TempDir()
	wuWrite(t, dir, "go.mod", "module svc\n\ngo 1.23\n")
	wuWrite(t, dir, "cmd/app/main.go", "package main\n\nimport \"svc/internal/handler\"\n\nfunc main() { handler.H() }\n")
	wuWrite(t, dir, "internal/handler/h.go", "package handler\n\nimport \"svc/internal/util\"\n\nfunc H() { util.U() }\n")
	wuWrite(t, dir, "internal/util/u.go", "package util\n\nfunc U() {}\n")

	if verdict, orphans := systemWireupGate(dir, "go"); verdict != WireupWired {
		t.Errorf("a fully-wired tree must be WireupWired; orphans=%v", orphans)
	}
}

// TestSystemWireupGateAbstainsWithoutMain: with no main package, wiring can't be rooted — the gate
// abstains (ok=true) rather than flag every package as an orphan.
func TestSystemWireupGateAbstainsWithoutMain(t *testing.T) {
	dir := t.TempDir()
	wuWrite(t, dir, "go.mod", "module lib\n\ngo 1.23\n")
	wuWrite(t, dir, "pkg/a/a.go", "package a\n\nfunc A() {}\n")

	if verdict, _ := systemWireupGate(dir, "go"); verdict != WireupUnverified {
		t.Error("a no-main library must be WireupUnverified (honest, not a silent clean)")
	}
}

// TestSystemWireupGateUnverifiedForNonGo (or-4y7.8): a non-Go language has no
// registered analyzer — the verdict is Unverified, NEVER the false "wireup clean"
// the old two-valued gate produced (no Go mains → abstain → pass).
func TestSystemWireupGateUnverifiedForNonGo(t *testing.T) {
	dir := t.TempDir()
	wuWrite(t, dir, "main.py", "def run(argv):\n    return 0\n")
	verdict, orphans := systemWireupGate(dir, "ruby")
	if verdict != WireupUnverified {
		t.Fatalf("an unanalyzable (non-Go) tree must be WireupUnverified, got %v", verdict)
	}
	if len(orphans) != 0 {
		t.Fatalf("Unverified carries no orphans, got %v", orphans)
	}
	// And it renders distinctly — never "wireup clean".
	line, _ := driftReport(spec.ExecutableSpec{}, proof.Report{}, verdict, orphans, nil)
	if strings.Contains(line, "wireup clean") || !strings.Contains(line, "unverified") {
		t.Fatalf("an Unverified tree must not report 'wireup clean': %q", line)
	}
}
