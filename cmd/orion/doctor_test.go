package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/modelfetch"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

func okLook(string) (string, error)   { return "/usr/bin/x", nil }
func missLook(string) (string, error) { return "", errors.New("not found") }

func statusOf(checks []doctorCheck, name string) checkStatus {
	for _, c := range checks {
		if c.Name == name {
			return c.Status
		}
	}
	return checkStatus("missing")
}

// TestDoctorChecksHealthy (or-ykz.18): a present data dir + available backend + no vendor
// agent → every component reports ok.
func TestDoctorChecksHealthy(t *testing.T) {
	checks := doctorChecks(t.TempDir(), okLook, "", false)
	for _, name := range []string{"data-dir", "context-store", "memory-store", "sandbox-backend", "agent-preset"} {
		if s := statusOf(checks, name); s != statusOK {
			t.Errorf("%s: got %s, want ok", name, s)
		}
	}
}

// TestDoctorFixCreatesDataDir: a missing data dir FAILs without --fix and is repaired with
// --fix (the known fault --fix repairs), after which the stores open.
func TestDoctorFixCreatesDataDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "sub")
	if s := statusOf(doctorChecks(missing, okLook, "", false), "data-dir"); s != statusFail {
		t.Fatalf("missing data dir without --fix: got %s, want fail", s)
	}
	checks := doctorChecks(missing, okLook, "", true)
	if s := statusOf(checks, "data-dir"); s != statusOK {
		t.Fatalf("missing data dir with --fix: got %s, want ok", s)
	}
	if s := statusOf(checks, "memory-store"); s != statusOK {
		t.Fatalf("memory-store after --fix: got %s, want ok", s)
	}
}

// TestDoctorSandboxWarnWhenMissing: no bwrap on PATH is a warn (degraded, not failed).
func TestDoctorSandboxWarnWhenMissing(t *testing.T) {
	if s := statusOf(doctorChecks(t.TempDir(), missLook, "", false), "sandbox-backend"); s != statusWarn {
		t.Errorf("no bwrap: got %s, want warn", s)
	}
}

// TestDoctorAgentPresetUnknownFails: ORION_AGENT set to an unknown preset is a fault.
func TestDoctorAgentPresetUnknownFails(t *testing.T) {
	if s := statusOf(doctorChecks(t.TempDir(), okLook, "definitely-not-a-preset", false), "agent-preset"); s != statusFail {
		t.Errorf("unknown preset: got %s, want fail", s)
	}
}

// or-c6zf.5 + or-o213 (opt-out): the embedder doctor probe's states —
// unprovisioned (ok + the enable hint), explicitly disabled (ok), on by
// default when provisioned (ok), explicitly-configured-but-broken (warn +
// the fetch hint), and explicit config provisioned (ok).
func TestEmbedderCheckStates(t *testing.T) {
	t.Setenv("ORION_MEMORY_EMBEDDER", "")
	if c := embedderCheck(t.TempDir()); c.Status != statusOK || !strings.Contains(c.Detail, "orion model fetch") {
		t.Fatalf("unset+unprovisioned must be ok with the enable hint: %+v", c)
	}

	t.Setenv("ORION_MEMORY_EMBEDDER", "off")
	if c := embedderCheck(t.TempDir()); c.Status != statusOK || !strings.Contains(c.Detail, "disabled") {
		t.Fatalf("explicit off must report disabled ok: %+v", c)
	}

	// Provisioned + unset env: ON by default (the opt-out flip).
	provDir := t.TempDir()
	for _, a := range modelfetch.BGEBaseAssets() {
		p := filepath.Join(provDir, "models", a.Name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(p, a.Size); err != nil { // sparse — the quick probe checks size, not content
			t.Skipf("cannot allocate sparse fixture: %v", err)
		}
	}
	t.Setenv("ORION_MEMORY_EMBEDDER", "")
	if c := embedderCheck(provDir); c.Status != statusOK || !strings.Contains(c.Detail, "on (default)") {
		t.Fatalf("provisioned + unset env must report semantic recall ON: %+v", c)
	}

	t.Setenv("ORION_MEMORY_EMBEDDER", "local")
	t.Setenv("ORION_MEMORY_MODEL_PATH", "/nonexistent")
	c := embedderCheck("")
	if c.Status != statusWarn || !strings.Contains(c.Detail, "orion model fetch") || !strings.Contains(c.Detail, "keyword+heat") {
		t.Fatalf("opted-in + missing assets must WARN with the recovery: %+v", c)
	}

	dir := t.TempDir()
	for _, a := range modelfetch.BGEBaseAssets() {
		p := filepath.Join(dir, a.Name)
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(p, a.Size); err != nil { // sparse — the quick probe checks size, not content
			t.Skipf("cannot allocate sparse fixture: %v", err)
		}
	}
	t.Setenv("ORION_MEMORY_MODEL_PATH", dir)
	if c := embedderCheck(""); c.Status != statusOK {
		t.Fatalf("provisioned assets must be ok: %+v", c)
	}
}

// or-ha0z: the divergence check — informational with nothing to compare; FAIL
// naming the key when pinned memory contradicts the ratified decision.
func TestDivergenceCheckStates(t *testing.T) {
	if c := divergenceCheck(t.TempDir()); c.Status != statusOK {
		t.Fatalf("empty data dir must be informational ok: %+v", c)
	}

	dir := t.TempDir()
	store, err := contextstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	oc := orchestrator.NewWithStore(store)
	ctx := context.Background()
	if _, err := oc.Submit(ctx, "Build an HTTP service that returns the current time."); err != nil {
		t.Fatal(err)
	}
	if err := oc.RecordAnswer(ctx, "response_format", "json"); err != nil {
		t.Fatal(err)
	}
	proj, _, err := store.CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o700); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(filepath.Join(dir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := mem.ForProject(proj.ID).Write(ctx, memory.Item{
		Tier: memory.MTM, Kind: memory.KindDecision, TrustTier: memory.TrustProof, Heat: 1.0,
		Content: "decision response_format = xml",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = id
	_ = mem.Close()
	_ = store.Close()

	c := divergenceCheck(dir)
	if c.Status != statusFail || !strings.Contains(c.Detail, "response_format") || !strings.Contains(c.Detail, "xml") || !strings.Contains(c.Detail, "json") {
		t.Fatalf("a contradicted decision must FAIL naming key + both values: %+v", c)
	}
}
