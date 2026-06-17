// Package sandbox provides per-WorkerSession workspace lifecycle management.
//
// Each Workspace is a directory tree under Options.Root that holds the
// repo checkout, harness, candidate patches, reports, and run metadata
// for one Orion run. Keys are sanitized to [A-Za-z0-9._-] before being
// used as directory names so untrusted external_id values cannot escape
// the sandbox root via path traversal (SPEC §10.4).
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Options configures Workspace provisioning.
type Options struct {
	// Root is the absolute sandbox root directory. In production this is
	// /sandbox-root (SPEC §10.1); in tests, t.TempDir() works.
	Root string
	// Key uniquely identifies this workspace within Root. Typically
	// <run_id>-<issue_internal_id> (SPEC §4.2). Untrusted; sanitized.
	Key string
}

// Workspace is a provisioned workspace directory tree.
type Workspace struct {
	// Path is the absolute workspace directory (Root/<sanitized-key>).
	Path string

	cleaned bool
}

// validKey matches characters that survive sanitization unchanged.
var validKey = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Subdirectories created under each workspace per SPEC §10.1.
var subdirs = []string{"repo", "harness", "patches", "reports", ".orion-meta"}

// Provision creates a new workspace directory tree under opts.Root.
//
// The key is sanitized: any character outside [A-Za-z0-9._-] is replaced
// with '_'. The resulting directory MUST be a direct child of an absolute
// Root path (no symlink traversal escape).
func Provision(opts Options) (*Workspace, error) {
	if opts.Root == "" {
		return nil, errors.New("sandbox: Root must be set")
	}
	if !filepath.IsAbs(opts.Root) {
		return nil, fmt.Errorf("sandbox: Root must be absolute, got %q", opts.Root)
	}
	if opts.Key == "" {
		return nil, errors.New("sandbox: Key must be set")
	}
	sanitized := validKey.ReplaceAllString(opts.Key, "_")
	path := filepath.Join(opts.Root, sanitized)

	// Defense-in-depth: confirm the resulting path is under Root after
	// resolution. If sanitization left anything traversal-like through
	// (it shouldn't, but belt-and-suspenders), reject.
	rel, err := filepath.Rel(opts.Root, path)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("sandbox: workspace path %q escapes root %q", path, opts.Root)
	}
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("sandbox: workspace already exists at %q", path)
	}
	if err := os.MkdirAll(path, 0o750); err != nil {
		return nil, fmt.Errorf("sandbox: mkdir %q: %w", path, err)
	}
	for _, sub := range subdirs {
		if err := os.MkdirAll(filepath.Join(path, sub), 0o750); err != nil {
			_ = os.RemoveAll(path)
			return nil, fmt.Errorf("sandbox: mkdir subdir %q: %w", sub, err)
		}
	}
	return &Workspace{Path: path}, nil
}

// Cleanup removes the workspace directory tree. Idempotent.
func (w *Workspace) Cleanup() error {
	if w == nil || w.cleaned {
		return nil
	}
	if err := os.RemoveAll(w.Path); err != nil {
		return fmt.Errorf("sandbox: cleanup %q: %w", w.Path, err)
	}
	w.cleaned = true
	return nil
}

// RunWithCleanup provisions a workspace, invokes fn with it, and guarantees
// cleanup runs whether fn returns nil, returns an error, or panics.
//
// If both fn and Cleanup return errors, fn's error is returned and the
// cleanup error is discarded; cleanup failures during a successful fn
// surface as the returned error.
func RunWithCleanup(opts Options, fn func(*Workspace) error) (retErr error) {
	ws, err := Provision(opts)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := ws.Cleanup(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()
	return fn(ws)
}
