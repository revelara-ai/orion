package conductor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ensureModDeps provisions a generated change's module dependencies HOST-side
// (or-mkxd): `go mod tidy` reconciles go.mod/go.sum with the diff's imports,
// and when they changed, `go mod download` pre-fetches into the host module
// cache — which the hermetic proof env reads (proofexec GOMODCACHE
// passthrough) while its GOPROXY stays off. Provision outside, prove inside.
// Returns whether go.mod/go.sum changed. Runs with the HOST env: this is the
// trusted generation side, where network is permitted — never under proof.
func ensureModDeps(ctx context.Context, dir string) (changed bool, err error) {
	if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr != nil {
		return false, nil // not a Go module — nothing to provision
	}
	before := modSums(dir)
	if out, terr := runHostGo(ctx, dir, "mod", "tidy"); terr != nil {
		return false, fmt.Errorf("dependency provisioning: go mod tidy: %w: %s", terr, out)
	}
	changed = modSums(dir) != before
	if changed {
		if out, derr := runHostGo(ctx, dir, "mod", "download"); derr != nil {
			return changed, fmt.Errorf("dependency provisioning: go mod download: %w: %s", derr, out)
		}
	}
	return changed, nil
}

// modSums is a cheap change detector over go.mod+go.sum contents.
func modSums(dir string) [32]byte {
	var b []byte
	for _, f := range []string{"go.mod", "go.sum"} {
		data, _ := os.ReadFile(filepath.Join(dir, f))
		b = append(b, data...)
		b = append(b, 0)
	}
	return sha256.Sum256(b)
}

func runHostGo(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", args...) // #nosec G204 -- fixed binary, fixed verbs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
