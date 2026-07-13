package harnessconfig

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Staged rollout for harness content (or-mvr.6, the inc-u12 Channel-File-291
// lesson: a bad content update must never deploy everywhere at once). A
// candidate config lives NEXT TO the stable one; a versioned manifest says
// what fraction of sites reads the candidate. The pick is DETERMINISTIC per
// site key (same module/projectType → same choice), so a canaried run is
// reproducible and a bad candidate hits only its fraction. Rollback is one
// command — drop the manifest, everything reads stable instantly. The Red
// Button (or-v9f.14) remains the global kill switch above this.

const (
	canaryManifest = "canary.yaml"
	candidateDir   = "candidate"
)

// Canary is the versioned rollout manifest.
type Canary struct {
	// Version is the monotonic config version being canaried (the audit
	// anchor for "which harness content produced this run").
	Version int `yaml:"version"`
	// Fraction of sites (by deterministic key hash) reading the candidate.
	Fraction float64 `yaml:"fraction"`
}

// loadCanary reads the manifest. ok=false when absent or invalid (invalid is
// also surfaced by Validate — a broken manifest must not half-deploy).
func loadCanary() (Canary, bool) {
	raw, err := os.ReadFile(filepath.Join(Dir(), canaryManifest))
	if err != nil {
		return Canary{}, false
	}
	c, err := parseCanary(raw)
	if err != nil {
		return Canary{}, false
	}
	return c, true
}

func parseCanary(raw []byte) (Canary, error) {
	var c Canary
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Canary{}, err
	}
	if c.Fraction < 0 || c.Fraction > 1 {
		return Canary{}, fmt.Errorf("canary fraction %v out of [0,1]", c.Fraction)
	}
	if c.Version <= 0 {
		return Canary{}, fmt.Errorf("canary version must be a positive integer, got %d", c.Version)
	}
	return c, nil
}

// canaryPick deterministically assigns a site key to the candidate cohort.
func canaryPick(key string, fraction float64) bool {
	if fraction <= 0 {
		return false
	}
	if fraction >= 1 {
		return true
	}
	// sha256 (not FNV): short keys must land uniformly — FNV-1a of small
	// inputs clusters in the top decile, which would put a 0.5 canary at ~0%.
	sum := sha256.Sum256([]byte(key))
	v := binary.BigEndian.Uint32(sum[:4])
	return float64(v)/float64(^uint32(0)) < fraction
}

// configPath resolves which copy of a config file a given site reads: the
// candidate copy when a canary is active, the site is in the cohort, AND the
// candidate file exists; the stable copy otherwise.
func configPath(name, siteKey string) string {
	stable := filepath.Join(Dir(), name)
	c, ok := loadCanary()
	if !ok || !canaryPick(siteKey, c.Fraction) {
		return stable
	}
	cand := filepath.Join(Dir(), candidateDir, name)
	if _, err := os.Stat(cand); err != nil {
		return stable // a canary without this candidate file canaries nothing
	}
	return cand
}

// Rollback is the one-command abort: it removes the manifest, so every site
// reads stable on its very next load. The candidate files stay on disk for
// the post-mortem. Idempotent.
func Rollback() error {
	err := os.Remove(filepath.Join(Dir(), canaryManifest))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// StartCanary writes the manifest (fraction ∈ [0,1], version > 0). The
// candidate files go in <dir>/candidate/ — reviewable and diffable against
// the stable copies beside them.
func StartCanary(version int, fraction float64) error {
	if _, err := parseCanary([]byte(fmt.Sprintf("version: %d\nfraction: %v\n", version, fraction))); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(Dir(), candidateDir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(Dir(), canaryManifest),
		[]byte(fmt.Sprintf("version: %d\nfraction: %v\n", version, fraction)), 0o644)
}

// Promote graduates the candidate: every candidate file replaces its stable
// counterpart, then the manifest is dropped. One command, no recompile.
func Promote() error {
	dir := Dir()
	entries, err := os.ReadDir(filepath.Join(dir, candidateDir))
	if err != nil {
		return fmt.Errorf("promote: no candidate config: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src := filepath.Join(dir, candidateDir, e.Name())
		dst := filepath.Join(dir, e.Name())
		b, rerr := os.ReadFile(src)
		if rerr != nil {
			return rerr
		}
		if werr := os.WriteFile(dst, b, 0o644); werr != nil {
			return werr
		}
	}
	return Rollback()
}

// CanaryStatus renders the rollout state for the CLI/doctor.
func CanaryStatus() string {
	c, ok := loadCanary()
	if !ok {
		return "no canary active — all sites read the stable config"
	}
	var files []string
	if entries, err := os.ReadDir(filepath.Join(Dir(), candidateDir)); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				files = append(files, e.Name())
			}
		}
	}
	return fmt.Sprintf("canary v%d at fraction %v — candidate files: %s", c.Version, c.Fraction, strings.Join(files, ", "))
}
