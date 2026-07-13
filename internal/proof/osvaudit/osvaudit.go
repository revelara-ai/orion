// Package osvaudit screens a module's dependencies against the OSV.dev
// known-vulnerability database (or-ykz.16, A15). Existence + provenance
// checks (dependencyprovenance) prove a dep is REAL; this proves it isn't
// KNOWN-BROKEN. Network-dependent by nature: an unreachable OSV yields a
// visible SKIP (the bar surfaces it), never a silent pass and never a block —
// only POSITIVE findings block delivery. Tool-binary provenance (cosign) is
// deliberately out of scope until signing infra exists (bead note).
package osvaudit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
)

// Finding is one dependency with known vulnerabilities.
type Finding struct {
	Module  string
	Version string
	IDs     []string // OSV vulnerability ids (GO-…, GHSA-…, CVE-…)
}

// Result of one audit run.
type Result struct {
	Checked  int
	Findings []Finding
	Skipped  string // non-empty → the audit did not run (offline, disabled, no go.mod)
}

// Summary renders the result for a phase line / escalation reason.
func (r Result) Summary() string {
	if r.Skipped != "" {
		return "OSV audit skipped: " + r.Skipped
	}
	if len(r.Findings) == 0 {
		return fmt.Sprintf("%d dependencies clean (OSV)", r.Checked)
	}
	parts := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		parts = append(parts, f.Module+"@"+f.Version+" ("+strings.Join(f.IDs, ", ")+")")
	}
	return "known-vulnerable dependencies: " + strings.Join(parts, "; ")
}

const defaultOSV = "https://api.osv.dev/v1/querybatch"

// Audit screens every require entry (direct AND indirect — a vulnerable
// transitive dep ships all the same) of root's go.mod against OSV.
func Audit(ctx context.Context, root string) Result {
	if os.Getenv("ORION_OSV") == "off" {
		return Result{Skipped: "disabled (ORION_OSV=off)"}
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod")) // #nosec G304 -- module root under proof control
	if err != nil {
		return Result{Skipped: "no go.mod at the artifact root"}
	}
	mf, err := modfile.ParseLax("go.mod", data, nil)
	if err != nil {
		return Result{Skipped: "go.mod unparsable: " + err.Error()}
	}
	if len(mf.Require) == 0 {
		return Result{Checked: 0} // stdlib-only artifact: clean by construction
	}

	type pkg struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	}
	type query struct {
		Package pkg    `json:"package"`
		Version string `json:"version"`
	}
	var queries []query
	var mods []Finding // parallel to queries: module identity for mapping results back
	for _, req := range mf.Require {
		v := strings.TrimPrefix(req.Mod.Version, "v")
		queries = append(queries, query{Package: pkg{Name: req.Mod.Path, Ecosystem: "Go"}, Version: v})
		mods = append(mods, Finding{Module: req.Mod.Path, Version: req.Mod.Version})
	}
	body, err := json.Marshal(map[string]any{"queries": queries})
	if err != nil {
		return Result{Skipped: "query build failed: " + err.Error()}
	}

	url := os.Getenv("ORION_OSV_URL")
	if url == "" {
		url = defaultOSV
	}
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Result{Skipped: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{Skipped: "OSV unreachable: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{Skipped: fmt.Sprintf("OSV answered status %d", resp.StatusCode)}
	}
	var out struct {
		Results []struct {
			Vulns []struct {
				ID string `json:"id"`
			} `json:"vulns"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Result{Skipped: "OSV response unparsable: " + err.Error()}
	}
	if len(out.Results) != len(queries) {
		return Result{Skipped: fmt.Sprintf("OSV returned %d results for %d queries", len(out.Results), len(queries))}
	}
	res := Result{Checked: len(queries)}
	for i, r := range out.Results {
		if len(r.Vulns) == 0 {
			continue
		}
		f := mods[i]
		for _, v := range r.Vulns {
			f.IDs = append(f.IDs, v.ID)
		}
		res.Findings = append(res.Findings, f)
	}
	return res
}
