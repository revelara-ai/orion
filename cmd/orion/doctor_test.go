package main

import (
	"errors"
	"path/filepath"
	"testing"
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
