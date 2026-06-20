// Package completeness is Orion's intent-completeness gate (or-sqw, PRD Phase A
// / Trace 1). It is DETERMINISTIC-FIRST: a rules-based required-decisions
// checklist across the eight executable-spec dimensions, unit-testable without a
// live LLM. Unresolved dimensions become OpenDecisions the Conductor grills the
// developer to resolve. An optional LLM enrichment pass (cassette-replayable) may
// add project-specific decisions later, but the gate never silently guesses.
//
// Manifesto: make intent complete before building — a dimension that affects a
// downstream control loop must be decided in the spec, not discovered in
// production.
package completeness

import (
	"regexp"
	"strings"
)

// Dimension is one of the eight executable-spec dimensions.
type Dimension string

const (
	DimFunctional    Dimension = "functional"
	DimScale         Dimension = "scale"
	DimObservability Dimension = "observability"
	DimOnCall        Dimension = "oncall"
	DimData          Dimension = "data"
	DimSLO           Dimension = "slo"
	DimSecurity      Dimension = "security"
	DimDependencies  Dimension = "dependencies"
)

// RequiredDecision is a checklist entry: a decision that must be resolved for a
// project type before the spec is complete.
type RequiredDecision struct {
	Key       string
	Dimension Dimension
	Question  string
	Fallback  string // human-readable fallback/default when unspecified
}

// OpenDecision is an unresolved RequiredDecision surfaced to the developer.
type OpenDecision struct {
	Key       string    `json:"key"`
	Dimension Dimension `json:"dimension"`
	Question  string    `json:"question"`
	Fallback  string    `json:"fallback,omitempty"`
}

// Analyzer computes open decisions for a project type. It holds no LLM and no
// state beyond its static checklist — the analysis is a pure function of
// (intent, answers).
type Analyzer struct {
	projectType string
	checklist   []RequiredDecision
}

// NewAnalyzer returns an analyzer for a project type. Unknown types fall back to
// the http-service checklist (the V2.0 greenfield path).
func NewAnalyzer(projectType string) *Analyzer {
	return &Analyzer{projectType: projectType, checklist: checklistFor(projectType)}
}

// Checklist returns a copy of the required-decisions checklist (deterministic).
func (a *Analyzer) Checklist() []RequiredDecision {
	out := make([]RequiredDecision, len(a.checklist))
	copy(out, a.checklist)
	return out
}

// Analyze returns the OpenDecisions remaining after applying answers and any
// values explicitly stated in the intent. Deterministic: no LLM, no guessing —
// a decision is resolved only by an explicit answer or an explicit statement in
// the intent.
func (a *Analyzer) Analyze(intent string, answers map[string]string) []OpenDecision {
	var open []OpenDecision
	for _, rd := range a.checklist {
		if v, ok := answers[rd.Key]; ok && strings.TrimSpace(v) != "" {
			continue
		}
		if resolvedFromIntent(intent, rd.Key) {
			continue
		}
		open = append(open, OpenDecision{
			Key:       rd.Key,
			Dimension: rd.Dimension,
			Question:  rd.Question,
			Fallback:  rd.Fallback,
		})
	}
	return open
}

// checklistFor is the rules-based required-decisions checklist per project type.
func checklistFor(projectType string) []RequiredDecision {
	// V2.0 ships the http-service (Go greenfield) checklist; other types fall back
	// to it until their checklists are added.
	switch projectType {
	default:
		_ = projectType // http-service and all unknown types use the V2.0 checklist
	}
	return []RequiredDecision{
		{"response_format", DimFunctional, "What response format should the service return (e.g. JSON, plain text)?", ""},
		{"timezone", DimFunctional, "Which timezone should times be reported in (e.g. UTC, local)?", ""},
		{"port", DimFunctional, "Which port should the service listen on?", ""},
		{"route", DimFunctional, "Which route/path serves the response (e.g. /time)?", ""},
		{"scale_profile", DimScale, "What is the expected traffic (requests over a window + request weight)?", "fallback preset: low | medium | high"},
		{"observability_signals", DimObservability, "Which signals are required (traces/metrics/logs) and where are they exported?", "tier-default signal set"},
		{"oncall_escalation", DimOnCall, "Who is contacted when it breaks, and what is the escalation path?", "single owner, log-only alert"},
		{"data_storage", DimData, "What persists, where, with what durability/retention and PII sensitivity?", "no persistence"},
		{"slo_targets", DimSLO, "What are the uptime/latency objectives and error budget?", "tier-default SLO"},
		{"security_model", DimSecurity, "What is the authn/z model and data classification?", "untrusted input, no regulated data"},
		{"dependencies", DimDependencies, "Which external services/APIs does it call, and their failure modes?", "none"},
	}
}

// Deterministic intent matchers: only resolve a decision when the intent states
// it explicitly. Narrow by design so the gate never guesses.
var (
	portRe = regexp.MustCompile(`(?i)\bport\s+\d{2,5}\b`)
	// Only formats the contract + proof pipeline actually support, so the two
	// gates share one vocabulary. An intent naming an unsupported format (xml,
	// protobuf) does NOT auto-resolve — it stays open and is asked, then rejected
	// loudly at contract assembly rather than silently mishandled.
	jsonRe  = regexp.MustCompile(`(?i)\b(json|plain ?text)\b`)
	tzRe    = regexp.MustCompile(`(?i)\b(utc|gmt|local time|[a-z]+/[a-z_]+)\b`)
	routeRe = regexp.MustCompile(`(?i)(\broute\b|\bpath\b|\bendpoint\b)\s+\S+|\s/[a-z0-9_\-/]+`)
)

func resolvedFromIntent(intent, key string) bool {
	switch key {
	case "port":
		return portRe.MatchString(intent)
	case "response_format":
		return jsonRe.MatchString(intent)
	case "timezone":
		return tzRe.MatchString(intent)
	case "route":
		return routeRe.MatchString(intent)
	default:
		// Non-functional dimensions are never inferred from a bare idea — they
		// require an explicit answer (PRD: decide in the spec, not in production).
		return false
	}
}

// ScaleThreshold is a concrete capacity target a scale fallback preset expands to.
type ScaleThreshold struct {
	RequestsPerWindow int    `json:"requests_per_window"`
	Window            string `json:"window"`
	RequestWeight     string `json:"request_weight"`
}

// ResolveScalePreset expands a fallback preset (low|medium|high) into a concrete
// numeric threshold so "fallback" still yields a testable capacity target.
func ResolveScalePreset(preset string) (ScaleThreshold, bool) {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "low":
		return ScaleThreshold{RequestsPerWindow: 60, Window: "minute", RequestWeight: "light"}, true
	case "medium":
		return ScaleThreshold{RequestsPerWindow: 1000, Window: "minute", RequestWeight: "moderate"}, true
	case "high":
		return ScaleThreshold{RequestsPerWindow: 10000, Window: "minute", RequestWeight: "heavy"}, true
	default:
		return ScaleThreshold{}, false
	}
}
