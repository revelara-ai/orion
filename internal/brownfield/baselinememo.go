package brownfield

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Baseline memoization (or-u595, or-6f0q lever 2): green-before on an
// unchanged HEAD tree is the same computation every run. GREEN verdicts only
// are cached (red may be environmental — the 2026-07-08 contention
// false-red); the key binds everything that could change the verdict:
// (HEAD tree-hash, scope patterns, skip set, go version). Soundness gates:
// the working tree must be CLEAN at baseline time (post-stash), else no
// memo read or write; entries expire after a TTL. The stamp rides the proof
// evidence — auditability is part of proof strength.

const baselineMemoTTL = 7 * 24 * time.Hour

// baselineMemoEnabled: default on; ORION_BASELINE_MEMO=off disables.
func baselineMemoEnabled() bool { return os.Getenv("ORION_BASELINE_MEMO") != "off" }

type baselineMemoEntry struct {
	Key       string `json:"key"`
	At        string `json:"at"` // RFC3339 of the fresh green run
	Toolchain string `json:"toolchain"`
	Command   string `json:"command"`
}

// baselineMemoKey derives the soundness key, or ok=false when the tree state
// cannot vouch (dirty tree, unresolvable hash).
func baselineMemoKey(ctx context.Context, repoDir string, scope, skip []string) (string, bool) {
	if !baselineMemoEnabled() {
		return "", false
	}
	if status, err := gitOutput(ctx, repoDir, "status", "--porcelain"); err != nil || strings.TrimSpace(status) != "" {
		return "", false // a dirty tree is not the HEAD tree — never memoize it
	}
	tree, err := gitOutput(ctx, repoDir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", false
	}
	s := append([]string(nil), scope...)
	k := append([]string(nil), skip...)
	sort.Strings(s)
	sort.Strings(k)
	sum := sha256.Sum256([]byte(strings.TrimSpace(tree) + "|" + strings.Join(s, ",") + "|" + strings.Join(k, ",") + "|" + runtime.Version()))
	return hex.EncodeToString(sum[:]), true
}

// baselineMemoPath stores the cache beside the repo's SHARED git dir so every
// worktree of the same repo shares baselines (same tree hash = same verdict).
func baselineMemoPath(ctx context.Context, repoDir string) (string, bool) {
	common, err := gitOutput(ctx, repoDir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", false
	}
	common = strings.TrimSpace(common)
	if !filepath.IsAbs(common) {
		common = filepath.Join(repoDir, common)
	}
	return filepath.Join(common, "orion-baseline-cache.json"), true
}

// loadBaselineMemo returns the cached GREEN baseline for the key, if fresh.
func loadBaselineMemo(ctx context.Context, repoDir, key string) (baselineMemoEntry, bool) {
	path, ok := baselineMemoPath(ctx, repoDir)
	if !ok {
		return baselineMemoEntry{}, false
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- repo-local cache under .git
	if err != nil {
		return baselineMemoEntry{}, false
	}
	var entries map[string]baselineMemoEntry
	if json.Unmarshal(raw, &entries) != nil {
		return baselineMemoEntry{}, false
	}
	e, ok := entries[key]
	if !ok {
		return baselineMemoEntry{}, false
	}
	at, err := time.Parse(time.RFC3339, e.At)
	if err != nil || time.Since(at) > baselineMemoTTL {
		return baselineMemoEntry{}, false
	}
	return e, true
}

// saveBaselineMemo records a FRESH GREEN baseline (best-effort).
func saveBaselineMemo(ctx context.Context, repoDir, key string, before TestResult) {
	if !before.Passed {
		return // GREEN only: red may be environmental
	}
	path, ok := baselineMemoPath(ctx, repoDir)
	if !ok {
		return
	}
	entries := map[string]baselineMemoEntry{}
	if raw, err := os.ReadFile(path); err == nil { // #nosec G304 -- repo-local cache
		_ = json.Unmarshal(raw, &entries)
	}
	entries[key] = baselineMemoEntry{Key: key, At: time.Now().UTC().Format(time.RFC3339), Toolchain: before.Toolchain, Command: before.Command}
	if b, err := json.MarshalIndent(entries, "", " "); err == nil {
		_ = os.WriteFile(path, b, 0o600)
	}
}

// cachedBaselineResult reconstructs the green TestResult with the audit stamp.
func cachedBaselineResult(e baselineMemoEntry) TestResult {
	return TestResult{
		Detected: true, Toolchain: e.Toolchain, Command: e.Command, Passed: true,
		Output: "baseline: cached green from a prior run at " + e.At + " (same tree/scope/skip/go — ORION_BASELINE_MEMO=off to force a fresh run)",
	}
}

// looksTimedOut classifies a suite failure as a per-binary timeout — go test
// panics with "test timed out" when -timeout fires (or-6wbl b).
func looksTimedOut(out string) bool {
	return strings.Contains(out, "test timed out")
}
