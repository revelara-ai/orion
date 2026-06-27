package health

import (
	"errors"
	"path/filepath"
	"testing"
)

func okLook(string) (string, error)   { return "/usr/bin/x", nil }
func missLook(string) (string, error) { return "", errors.New("not found") }

func statusOf(r Report, name string) Status {
	for _, c := range r.All() {
		if c.Name == name {
			return c.Status
		}
	}
	return Status("missing")
}

// TestProbeHealthy (or-gik.1): a present data dir + available tools + no vendor agent → the core
// checks all report ok.
func TestProbeHealthy(t *testing.T) {
	r := Probe(Options{DataDir: t.TempDir(), LookPath: okLook})
	for _, name := range []string{
		"data-dir", "context-store", "memory-store", "sandbox-backend",
		"tracker", "lsp gate", "agent-preset", "proof harness",
	} {
		if s := statusOf(r, name); s != OK {
			t.Errorf("%s: got %s, want ok", name, s)
		}
	}
}

// TestProbeMissingDataDir: a missing data dir fails, and the store checks are omitted (they
// can't open against a dir that isn't there).
func TestProbeMissingDataDir(t *testing.T) {
	r := Probe(Options{DataDir: filepath.Join(t.TempDir(), "nope"), LookPath: okLook})
	if statusOf(r, "data-dir") != Fail {
		t.Error("a missing data dir should fail")
	}
	if statusOf(r, "context-store") != "missing" {
		t.Error("context-store should be omitted when the data dir is missing")
	}
}

// TestProbeDegradedTools: absent optional tools degrade to warn (not fail).
func TestProbeDegradedTools(t *testing.T) {
	r := Probe(Options{DataDir: t.TempDir(), LookPath: missLook})
	for _, name := range []string{"sandbox-backend", "tracker", "lsp gate"} {
		if statusOf(r, name) != Warn {
			t.Errorf("%s should warn when its tool is absent", name)
		}
	}
}

func TestProbeAgentUnknownFails(t *testing.T) {
	r := Probe(Options{DataDir: t.TempDir(), LookPath: okLook, AgentEnv: "definitely-not-a-preset"})
	if statusOf(r, "agent-preset") != Fail {
		t.Error("an unknown agent preset should fail")
	}
}

// TestProbePolarisInjected: the polaris row is contributed only when the caller injects a probe
// (so the TUI can pass a cached probe and `orion status` a live one).
func TestProbePolarisInjected(t *testing.T) {
	called := false
	r := Probe(Options{DataDir: t.TempDir(), LookPath: okLook, Polaris: func() Check {
		called = true
		return Check{"polaris", OK, "injected"}
	}})
	if !called || statusOf(r, "polaris") != OK {
		t.Error("an injected Polaris probe should be invoked and included")
	}
	if statusOf(Probe(Options{DataDir: t.TempDir(), LookPath: okLook}), "polaris") != "missing" {
		t.Error("polaris should be absent when no probe is injected")
	}
}

func TestReportSummary(t *testing.T) {
	r := Probe(Options{DataDir: t.TempDir(), LookPath: missLook})
	ok, warn, fail := r.Summary()
	if ok+warn+fail != len(r.All()) {
		t.Errorf("summary %d+%d+%d != %d total", ok, warn, fail, len(r.All()))
	}
	if warn == 0 {
		t.Error("missLook should produce at least one warn")
	}
}
