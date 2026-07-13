package main

import (
	"os"
	"path/filepath"
	"testing"
)

// or-ykz.15: the CLI save→mutate→rollback round-trip restores the exact tree.
func TestCmdCheckpointRoundTrip(t *testing.T) {
	wt := t.TempDir()
	t.Chdir(wt)
	t.Setenv("ORION_DATA_DIR", t.TempDir())

	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rc := cmdCheckpoint([]string{"save", "t1"}); rc != 0 {
		t.Fatalf("save exit %d", rc)
	}

	// Mutate + add a file.
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "junk.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if rc := cmdCheckpoint([]string{"rollback", "t1"}); rc != 0 {
		t.Fatalf("rollback exit %d", rc)
	}
	if b, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(b) != "original\n" {
		t.Fatalf("mutation not reverted: %q", b)
	}
	if _, err := os.Stat(filepath.Join(wt, "junk.txt")); !os.IsNotExist(err) {
		t.Fatal("turn-added file must be removed on rollback")
	}

	// Guardrails: bad usage + rollback to a missing checkpoint.
	if rc := cmdCheckpoint([]string{"save"}); rc != 2 {
		t.Fatalf("missing id must be a usage error, got %d", rc)
	}
	if rc := cmdCheckpoint([]string{"rollback", "nope"}); rc != 1 {
		t.Fatalf("rollback to missing checkpoint must fail, got %d", rc)
	}
}
