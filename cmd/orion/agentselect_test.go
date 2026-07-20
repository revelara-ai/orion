package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// or-wmv1: /agent set validates, applies immediately, and persists; startup
// restores the persisted choice but the explicit env var always wins; clear
// removes both; an unknown/uninstalled chain is refused with the presets named.
func TestAgentSelectLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORION_DATA_DIR", dir)
	t.Setenv("ORION_AGENT", "")

	// Unknown preset refused, nothing applied or persisted.
	out := agentCommandText("set not-a-real-agent")
	if !strings.Contains(out, "no usable agent") || !strings.Contains(out, "claude") {
		t.Fatalf("an unusable chain must be refused naming the presets: %q", out)
	}
	if os.Getenv("ORION_AGENT") != "" {
		t.Fatal("a refused set must not touch ORION_AGENT")
	}
	if _, err := os.Stat(agentPrefPath(dir)); err == nil {
		t.Fatal("a refused set must not persist")
	}

	// show reflects the native default.
	if out := agentCommandText("show"); !strings.Contains(out, "native provider") {
		t.Fatalf("unset must show the native provider: %q", out)
	}

	// A valid set requires the CLI on PATH; fake one.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gemini"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	out = agentCommandText("set gemini")
	if !strings.Contains(out, "coding agent set: gemini") {
		t.Fatalf("a valid set must apply: %q", out)
	}
	if os.Getenv("ORION_AGENT") != "gemini" {
		t.Fatal("set must apply to the live environment")
	}
	if b, err := os.ReadFile(agentPrefPath(dir)); err != nil || strings.TrimSpace(string(b)) != "gemini" {
		t.Fatalf("set must persist the choice: %v %q", err, b)
	}

	// Startup restore honors the persisted choice only when the env is unset.
	t.Setenv("ORION_AGENT", "")
	restoreAgentPref(dir)
	if os.Getenv("ORION_AGENT") != "gemini" {
		t.Fatal("startup must restore the persisted choice")
	}
	t.Setenv("ORION_AGENT", "codex")
	restoreAgentPref(dir)
	if os.Getenv("ORION_AGENT") != "codex" {
		t.Fatal("the explicit env var must always win over the persisted choice")
	}

	// clear removes both.
	if out := agentCommandText("clear"); !strings.Contains(out, "native provider") {
		t.Fatalf("clear must report the native fallback: %q", out)
	}
	if os.Getenv("ORION_AGENT") != "" {
		t.Fatal("clear must unset the live environment")
	}
	if _, err := os.Stat(agentPrefPath(dir)); err == nil {
		t.Fatal("clear must remove the persisted choice")
	}
	t.Setenv("ORION_AGENT", "")
	restoreAgentPref(dir)
	if os.Getenv("ORION_AGENT") != "" {
		t.Fatal("nothing persisted → nothing restored")
	}
}
