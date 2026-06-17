package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvisionSanitizesKey(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		in   string
		want string
	}{
		{"abc123", "abc123"},
		{"with spaces", "with_spaces"},
		{"path/with/slashes", "path_with_slashes"},
		{"weird!@#$chars", "weird____chars"},
		{"dots.and-dashes_ok", "dots.and-dashes_ok"},
		{"../../etc", ".._.._etc"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			ws, err := Provision(Options{Root: root, Key: tc.in})
			if err != nil {
				t.Fatalf("Provision: %v", err)
			}
			defer func() { _ = ws.Cleanup() }()
			base := filepath.Base(ws.Path)
			if base != tc.want {
				t.Errorf("sanitize(%q) base = %q, want %q", tc.in, base, tc.want)
			}
		})
	}
}

func TestProvisionRejectsTraversalRoot(t *testing.T) {
	if _, err := Provision(Options{Root: "../../tmp/orion-test", Key: "x"}); err == nil {
		t.Fatal("expected error for relative root, got nil")
	}
	if _, err := Provision(Options{Root: "", Key: "x"}); err == nil {
		t.Fatal("expected error for empty root, got nil")
	}
}

func TestProvisionCreatesExpectedDirs(t *testing.T) {
	root := t.TempDir()
	ws, err := Provision(Options{Root: root, Key: "k1"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer func() { _ = ws.Cleanup() }()
	for _, sub := range []string{"repo", "harness", "patches", "reports", ".orion-meta"} {
		path := filepath.Join(ws.Path, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("missing %s: %v", sub, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s not a dir", sub)
		}
	}
}

func TestProvisionPathStaysInsideRoot(t *testing.T) {
	root := t.TempDir()
	ws, err := Provision(Options{Root: root, Key: "k2"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer func() { _ = ws.Cleanup() }()
	if !strings.HasPrefix(ws.Path, root) {
		t.Fatalf("workspace %q escaped root %q", ws.Path, root)
	}
}

func TestCleanupRemovesWorkspace(t *testing.T) {
	root := t.TempDir()
	ws, err := Provision(Options{Root: root, Key: "k3"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if _, err := os.Stat(ws.Path); err != nil {
		t.Fatalf("workspace not created: %v", err)
	}
	if err := ws.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(ws.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace not removed: stat err = %v", err)
	}
}

func TestCleanupIdempotent(t *testing.T) {
	root := t.TempDir()
	ws, err := Provision(Options{Root: root, Key: "k4"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := ws.Cleanup(); err != nil {
		t.Fatalf("first Cleanup: %v", err)
	}
	if err := ws.Cleanup(); err != nil {
		t.Fatalf("second Cleanup must be no-op, got %v", err)
	}
}

func TestRunWithCleanupFiresOnSuccess(t *testing.T) {
	root := t.TempDir()
	var observed string
	err := RunWithCleanup(Options{Root: root, Key: "ok"}, func(ws *Workspace) error {
		observed = ws.Path
		return nil
	})
	if err != nil {
		t.Fatalf("RunWithCleanup: %v", err)
	}
	if _, err := os.Stat(observed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup didn't fire on success: stat err = %v", err)
	}
}

func TestRunWithCleanupFiresOnFailure(t *testing.T) {
	root := t.TempDir()
	var observed string
	wantErr := errors.New("boom")
	err := RunWithCleanup(Options{Root: root, Key: "fail"}, func(ws *Workspace) error {
		observed = ws.Path
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if _, err := os.Stat(observed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup didn't fire on failure: stat err = %v", err)
	}
}

func TestProvisionDuplicateKeyFails(t *testing.T) {
	root := t.TempDir()
	ws1, err := Provision(Options{Root: root, Key: "dup"})
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	defer func() { _ = ws1.Cleanup() }()
	if _, err := Provision(Options{Root: root, Key: "dup"}); err == nil {
		t.Fatal("expected duplicate key error, got nil")
	}
}
