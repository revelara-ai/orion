// Package reliabilityscan runs Orion's reliability detector fleet against a
// target and writes the findings as risks (or-tei, PRD C3/C5). In production the
// fleet is the rvl:* specialist agents dispatched via agent-runtime; V2.0 ships
// deterministic static detectors (the same checks, no LLM) so the scan is
// reproducible. Findings drive the risk register and the reliability-tier
// classification (which calibrates proof rigor + delivery).
//
// Manifesto: embedded SRE expertise; calibrated rigor, not blind maximization.
package reliabilityscan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// Finding is one detector result.
type Finding struct {
	Detector  string `json:"detector"`
	Risk      string `json:"risk"`
	Severity  string `json:"severity"` // low | medium | high
	Component string `json:"component"`
}

// detector is a deterministic reliability check over the artifact source.
type detector struct {
	name string
	scan func(src string) []Finding
}

var secretRe = regexp.MustCompile(`(?i)(password|secret|api[_-]?key|token)\s*[:=]\s*["'][^"']+["']`)

// fleet is the V2.0 deterministic detector set (rvl:* analogues).
var fleet = []detector{
	{"rvl:resilience-pro", func(src string) []Finding {
		if !strings.Contains(src, "ReadHeaderTimeout") {
			return []Finding{{"rvl:resilience-pro", "missing ReadHeaderTimeout (slowloris exposure)", "high", "http.Server"}}
		}
		return nil
	}},
	{"rvl:capacity-planning-pro", func(src string) []Finding {
		if !strings.Contains(src, "ReadTimeout") || !strings.Contains(src, "WriteTimeout") {
			return []Finding{{"rvl:capacity-planning-pro", "missing read/write timeouts (unbounded request handling)", "medium", "http.Server"}}
		}
		return nil
	}},
	{"rvl:observability-pro", func(src string) []Finding {
		if !strings.Contains(src, "log.") && !strings.Contains(src, "slog") && !strings.Contains(src, "metrics") {
			return []Finding{{"rvl:observability-pro", "no structured logging/metrics on the request path", "medium", "handler"}}
		}
		return nil
	}},
	{"rvl:security-supply-chain-pro", func(src string) []Finding {
		if secretRe.MatchString(src) {
			return []Finding{{"rvl:security-supply-chain-pro", "hardcoded secret/credential in source", "high", "source"}}
		}
		return nil
	}},
}

// ScanArtifact runs the detector fleet over an artifact directory's main.go.
func ScanArtifact(artifactDir string) ([]Finding, error) {
	b, err := os.ReadFile(filepath.Join(artifactDir, "main.go"))
	if err != nil {
		return nil, err
	}
	src := string(b)
	var findings []Finding
	for _, d := range fleet {
		findings = append(findings, d.scan(src)...)
	}
	return findings, nil
}

// ScanAndRecord scans and writes the findings to the risk register (persisted in
// the Context Store; Polaris risk write-back lands with the connector write path).
func ScanAndRecord(ctx context.Context, store *contextstore.Store, projectID, artifactDir string) ([]Finding, error) {
	findings, err := ScanArtifact(artifactDir)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(findings)
	if err != nil {
		return nil, err
	}
	if err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.PolarisContext().Upsert(ctx, projectID, "risks", string(payload), 0)
	}); err != nil {
		return nil, err
	}
	return findings, nil
}

// LoadRisks retrieves the recorded risks for a project.
func LoadRisks(ctx context.Context, store *contextstore.Store, projectID string) ([]Finding, error) {
	var findings []Finding
	err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		e, ok, err := tx.PolarisContext().Get(ctx, projectID, "risks")
		if err != nil || !ok {
			return err
		}
		return json.Unmarshal([]byte(e.Payload), &findings)
	})
	return findings, err
}

// DeriveDimensions maps scan findings to reliability-tier risk dimensions (so a
// scan can inform the tier classification that calibrates proof + delivery).
func DeriveDimensions(findings []Finding) reliabilitytier.RiskDimensions {
	d := reliabilitytier.RiskDimensions{Reversible: true}
	for _, f := range findings {
		switch f.Detector {
		case "rvl:security-supply-chain-pro":
			d.DataSensitivity = 2 // secret exposure → treat as sensitive
		case "rvl:capacity-planning-pro":
			if d.ConcurrencyExposure < 1 {
				d.ConcurrencyExposure = 1
			}
		case "rvl:resilience-pro":
			if d.BlastRadius < 1 {
				d.BlastRadius = 1
			}
		}
	}
	return d
}
