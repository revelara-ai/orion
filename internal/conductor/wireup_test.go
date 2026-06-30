package conductor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	ok, orphans := systemWireupGate(dir)
	if ok {
		t.Error("an unwired package must fail the wireup gate")
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

	if ok, orphans := systemWireupGate(dir); !ok {
		t.Errorf("a fully-wired tree must pass; orphans=%v", orphans)
	}
}

// TestSystemWireupGateAbstainsWithoutMain: with no main package, wiring can't be rooted — the gate
// abstains (ok=true) rather than flag every package as an orphan.
func TestSystemWireupGateAbstainsWithoutMain(t *testing.T) {
	dir := t.TempDir()
	wuWrite(t, dir, "go.mod", "module lib\n\ngo 1.23\n")
	wuWrite(t, dir, "pkg/a/a.go", "package a\n\nfunc A() {}\n")

	if ok, _ := systemWireupGate(dir); !ok {
		t.Error("a no-main library must abstain (ok=true)")
	}
}
