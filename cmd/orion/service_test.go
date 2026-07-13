package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// TestServiceUnitGeneration (or-v9f.25b acceptance): the generated unit
// points at `orion boot --stay`, delegates restarts to the init system, and
// passes systemd-analyze verify where available.
func TestServiceUnitGeneration(t *testing.T) {
	path, content, err := serviceUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("unit path must resolve")
	}
	for _, want := range []string{"boot --stay", "Restart=on-failure"} {
		if !strings.Contains(content, want) {
			t.Fatalf("unit must contain %q:\n%s", want, content)
		}
	}
	if bin, lerr := exec.LookPath("systemd-analyze"); lerr == nil {
		f := filepath.Join(t.TempDir(), "orion.service")
		if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, verr := exec.Command(bin, "verify", f).CombinedOutput(); verr != nil && strings.Contains(string(out), "orion.service") && strings.Contains(string(out), "rror") {
			t.Fatalf("systemd-analyze verify rejected the unit:\n%s", out)
		}
	}
}

// TestRunIncompleteDetection (or-v9f.25b): a persisted run with no terminal
// event is incomplete; a closed one is not; no events is not.
func TestRunIncompleteDetection(t *testing.T) {
	ctx := context.Background()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	add := func(run, phase, status string) {
		if err := s.AppendRunEvent(ctx, contextstore.RunEvent{ProjectID: "p", RunID: run, Phase: phase, Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	add("run-open", "Run", "running")
	add("run-open", "Generate", "running")
	add("run-closed", "Run", "running")
	add("run-closed", "Run", "done")

	if inc, err := runIncomplete(ctx, s, "run-open"); err != nil || !inc {
		t.Fatalf("an unclosed run is incomplete: %v %v", inc, err)
	}
	if inc, err := runIncomplete(ctx, s, "run-closed"); err != nil || inc {
		t.Fatalf("a closed run is complete: %v %v", inc, err)
	}
	if inc, _ := runIncomplete(ctx, s, "run-none"); inc {
		t.Fatal("no events → nothing to resume")
	}
}
