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

// NewAnalyzer returns an analyzer for a project type. A registered type contributes
// its FUNCTIONAL decisions (what it does + how it is invoked); EVERY type also gets
// the universal reliability dimensions. An UNREGISTERED type gets only the universal
// dimensions — it is never grilled on HTTP specifics (route/port/timezone) it may not
// have. (Registering more types — gRPC, worker, CLI — is how the harness generalizes
// the front door; the type is currently passed by the conductor.)
func NewAnalyzer(projectType string) *Analyzer {
	return &Analyzer{projectType: projectType, checklist: checklistFor(projectType)}
}

// RegisteredProjectType reports whether a project type has a functional checklist.
func RegisteredProjectType(projectType string) bool {
	return len(functionalDecisions(projectType)) > 0
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

// checklistFor is the required-decisions checklist for a project type: its
// type-specific FUNCTIONAL decisions followed by the universal reliability
// dimensions every project must resolve.
func checklistFor(projectType string) []RequiredDecision {
	return append(functionalDecisions(projectType), universalDecisions()...)
}

// functionalDecisions are the per-TYPE decisions about what the software does and
// how it is invoked. Only registered types contribute them; an unregistered type
// returns none (it gets only the universal dimensions, never HTTP route/port/timezone
// questions it may not have). This is the registry that generalizes the front door —
// add a case (gRPC, worker, CLI, library) to elicit that type's functional spec.
func functionalDecisions(projectType string) []RequiredDecision {
	switch projectType {
	case "http-service", "": // "" defaults to http-service (the V2.0 greenfield path)
		return []RequiredDecision{
			{"response_format", DimFunctional, "What response format should the service return (e.g. JSON, plain text)?", ""},
			{"timezone", DimFunctional, "Which timezone should times be reported in (e.g. UTC, local)?", ""},
			{"port", DimFunctional, "Which port should the service listen on?", ""},
			{"route", DimFunctional, "Which route/path serves the response (e.g. /time)?", ""},
		}
	default:
		return nil
	}
}

// universalDecisions are the cross-cutting reliability dimensions EVERY project must
// resolve regardless of type. They are domain-neutral (no HTTP/time assumptions).
func universalDecisions() []RequiredDecision {
	return []RequiredDecision{
		{"scale_profile", DimScale, "What is the expected traffic/throughput (work over a window + per-unit weight)?", "fallback preset: low | medium | high"},
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
	portRe = regexp.MustCompile(`(?i)\bport\s+(\d{2,5})\b`)
	// Only formats the contract + proof pipeline actually support, so the two
	// gates share one vocabulary. An intent naming an unsupported format (xml,
	// protobuf) does NOT auto-resolve — it stays open and is asked, then rejected
	// loudly at contract assembly rather than silently mishandled.
	jsonRe  = regexp.MustCompile(`(?i)\b(json|plain ?text)\b`)
	tzRe    = regexp.MustCompile(`(?i)\b(utc|gmt|local time|[a-z]+/[a-z_]+)\b`)
	// A path is recognized only after a route/path/endpoint keyword OR at a word
	// start (string start or whitespace) — so "client/server" or "and/or" do NOT
	// falsely resolve a route (the gate never guesses).
	routeRe = regexp.MustCompile(`(?i)(?:\b(?:route|path|endpoint)\s+|(?:^|\s))(/[a-z0-9_\-/]+)`)
)

// extractFromIntent returns the value a functional decision takes when the intent
// states it explicitly, and whether it did. Narrow + deterministic by design — the
// gate never guesses; it reads back only what the intent literally says. This is
// what makes an intent-stated decision USABLE rather than dropped-without-a-value
// (the or-jh7 bug, where Analyze removed it from OpenDecisions but nothing recorded
// the value, so spec.Compile then errored "unresolved").
func extractFromIntent(intent, key string) (string, bool) {
	switch key {
	case "port":
		if m := portRe.FindStringSubmatch(intent); m != nil {
			return m[1], true
		}
	case "response_format":
		if m := jsonRe.FindStringSubmatch(intent); m != nil {
			if strings.Contains(strings.ToLower(m[1]), "json") {
				return "json", true
			}
			return "text", true
		}
	case "timezone":
		if m := tzRe.FindStringSubmatch(intent); m != nil {
			return normalizeTZ(m[1]), true
		}
	case "route":
		if m := routeRe.FindStringSubmatch(intent); m != nil {
			return m[1], true
		}
	}
	// Non-functional dimensions are never inferred from a bare idea — they require an
	// explicit answer (PRD: decide in the spec, not in production).
	return "", false
}

func resolvedFromIntent(intent, key string) bool {
	_, ok := extractFromIntent(intent, key)
	return ok
}

// normalizeTZ canonicalizes a stated timezone to the vocabulary the spec uses.
func normalizeTZ(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "utc":
		return "UTC"
	case "gmt":
		return "GMT"
	case "local time", "local":
		return "local"
	default:
		return s // an IANA zone (e.g. America/New_York) is kept verbatim
	}
}

// IntentValues returns, for each checklist key the intent states explicitly, the
// value it states — so the flow can record intent-resolved decisions instead of
// dropping them. Deterministic and side-effect-free (or-jh7).
func (a *Analyzer) IntentValues(intent string) map[string]string {
	out := map[string]string{}
	for _, rd := range a.checklist {
		if v, ok := extractFromIntent(intent, rd.Key); ok {
			out[rd.Key] = v
		}
	}
	return out
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
