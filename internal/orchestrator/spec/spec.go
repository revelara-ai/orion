// Package spec compiles ratified developer decisions into Orion's executable
// spec (or-gbd, PRD Trace 1 A6–A7). The spec carries a machine-checkable
// ResponseContract (a JSON-Schema-shaped object) DERIVED DETERMINISTICALLY from
// human-approved decisions — it lives in the proof-adjacent control plane, never
// authored by a generation agent. The spec is hashed and anchored so the proof
// domain can read it back and verify it was not tampered with.
//
// Manifesto: intent as an executable contract; proof reads the spec directly.
package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// ResponseContract is the machine-checkable contract the proof domain validates a
// running artifact against. Derived from the approved functional decisions. The
// scalar fields (ContentType/Route/Port/TimeZone/Schema) describe the default
// case; Cases is the full behavioral case set the proof executes (>=1, always
// including the default). Cases is omitted from the anchor for scalar-only specs
// (see ComputeHash) so legacy specs hash identically.
type ResponseContract struct {
	ContentType string           `json:"content_type"`
	Route       string           `json:"route"`
	Port        int              `json:"port"`
	TimeZone    string           `json:"timezone"`
	Schema      map[string]any   `json:"schema"`
	Cases       []BehavioralCase `json:"cases,omitempty"`
}

// Dimension is a typed, persisted spec dimension (one per spec dimension).
type Dimension struct {
	Name      completeness.Dimension `json:"name"`
	Values    map[string]string      `json:"values"`
	ValueKind string                 `json:"value_kind"` // precise | fallback_preset | unresolved
}

// ExecutableSpec is the immutable, hashed contract the build is held to.
type ExecutableSpec struct {
	Intent           string            `json:"intent"`
	Decisions        map[string]string `json:"decisions"`
	Dimensions       []Dimension       `json:"dimensions"`
	ResponseContract ResponseContract  `json:"response_contract"`
	Requirements     []Requirement     `json:"requirements,omitempty"`
	Hash             string            `json:"hash"`
}

// Compile builds an ExecutableSpec from an intent and a complete set of answers.
// Every checklist decision must be present (callers apply fallback presets before
// compiling); a missing decision is a programming error, not a silent default.
// kinds maps a decision key to its value kind (precise|fallback_preset).
func Compile(intent string, answers map[string]string, kinds map[string]string, checklist []completeness.RequiredDecision, reqs []Requirement) (ExecutableSpec, error) {
	for _, rd := range checklist {
		if strings.TrimSpace(answers[rd.Key]) == "" {
			return ExecutableSpec{}, fmt.Errorf("cannot compile spec: decision %q (%s) is unresolved", rd.Key, rd.Dimension)
		}
	}
	// Every requirement must lower to executable cases, or it can't be proven and
	// must not be anchored (the or-y9d invariant, enforced at compile).
	for i := range reqs {
		reqs[i].SetIDs()
		if err := ValidateRequirement(reqs[i]); err != nil {
			return ExecutableSpec{}, fmt.Errorf("cannot compile spec: %w", err)
		}
	}

	rc, err := buildResponseContract(answers)
	if err != nil {
		return ExecutableSpec{}, err
	}
	rc.Cases = buildCases(rc, reqs)

	// A spec whose cases contradict each other cannot be implemented, so it must
	// never anchor (or-v9f.2). Checked over the FULL case set — including the
	// default case synthesized from the scalar contract — so a requirement that
	// contradicts an answered decision is caught here too, while the developer is
	// still in the conversation. This gates preview, ratify, and recall alike.
	if cs := FindContradictions(rc.Cases); len(cs) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "cannot compile spec: %d contradiction(s) — resolve them with the developer before ratifying:", len(cs))
		for _, c := range cs {
			fmt.Fprintf(&b, "\n  %s: cases %s and %s contradict — %s", c.Request, c.CaseA, c.CaseB, c.Reason)
		}
		return ExecutableSpec{}, fmt.Errorf("%s", b.String())
	}

	dims := buildDimensions(answers, kinds, checklist)

	s := ExecutableSpec{
		Intent:           strings.TrimSpace(intent),
		Decisions:        cloneStrMap(answers),
		Dimensions:       dims,
		ResponseContract: rc,
		Requirements:     reqs,
	}
	s.Hash = s.ComputeHash()
	return s, nil
}

// buildCases returns the behavioral cases the proof executes: the default case
// derived from the scalar contract, plus every requirement's cases, sorted by ID
// for determinism.
func buildCases(rc ResponseContract, reqs []Requirement) []BehavioralCase {
	var cases []BehavioralCase
	// Synthesize the happy-path case only for an HTTP contract (one with a route or
	// content type). A non-HTTP/minimal contract has no implied GET case — its
	// behavioral cases come from the declared requirements (or-3ba.5).
	if rc.Route != "" || rc.ContentType != "" {
		cases = append(cases, defaultCase(rc))
	}
	for _, r := range reqs {
		for _, c := range r.Cases {
			c.EnsureID()
			cases = append(cases, c)
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases
}

// defaultCase is the happy-path case implied by the scalar contract: GET the route
// returns 200 + the content type. It carries NO body assertion: a scalar contract
// declares the response STATUS and CONTENT-TYPE but not the body's SHAPE. Body
// behavior must be DECLARED via requirements/cases (e.g. the time service declares
// its "time" key) — the happy path is never assumed to be a timestamp. (This is the
// general-harness fix: a non-time service is no longer held to return an RFC3339
// "time" it never promised.)
func defaultCase(rc ResponseContract) BehavioralCase {
	c := BehavioralCase{
		Request: RequestShape{Method: "GET", Path: rc.Route},
		Expect:  ExpectShape{Status: 200, ContentType: rc.ContentType},
	}
	c.EnsureID()
	return c
}

// ComputeHash is the deterministic anchor over the spec content (excluding the
// hash field itself). The proof domain recomputes this on recall to verify the
// anchor.
func (s ExecutableSpec) ComputeHash() string {
	c := s
	c.Hash = ""
	// Scalar-only specs (no explicit Requirements) anchor EXACTLY as before the
	// case model existed: the derived default case is recomputed on recall, so it
	// is excluded from the anchor. This keeps every already-anchored legacy spec
	// hash-stable. Specs WITH requirements anchor their full case set (their
	// meaning genuinely changed, so a new hash is correct).
	if len(c.Requirements) == 0 {
		c.ResponseContract.Cases = nil
	}
	b, _ := json.Marshal(c) // map keys marshal sorted; Dimensions pre-sorted
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// VerifyAnchor reports whether the spec's stored hash matches its content.
func (s ExecutableSpec) VerifyAnchor() bool {
	return s.Hash != "" && s.Hash == s.ComputeHash()
}

func buildResponseContract(a map[string]string) (ResponseContract, error) {
	// Non-HTTP project types (CLI, library, worker) raise no HTTP response
	// format/route/port decisions, so those answers are absent. Produce a minimal
	// contract instead of erroring on a missing response_format — the behavioral
	// cases come entirely from the developer's declared requirements. (or-3ba.5:
	// a non-HTTP spec assembles + compiles.)
	if strings.TrimSpace(a["response_format"]) == "" && strings.TrimSpace(a["route"]) == "" {
		return ResponseContract{}, nil
	}
	rc := ResponseContract{
		Route:    a["route"],
		TimeZone: a["timezone"],
	}
	if p := strings.TrimSpace(a["port"]); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return ResponseContract{}, fmt.Errorf("port %q is not a number", p)
		}
		rc.Port = n
	}
	tok, err := normalizeResponseFormat(a["response_format"])
	if err != nil {
		return ResponseContract{}, err
	}
	switch tok {
	case "json":
		rc.ContentType = "application/json"
		// A generic JSON-object schema. The specific shape (which keys, which types) is
		// NOT assumed here — it is declared by the developer's requirements/cases. The
		// old schema hardcoded a required "time" string, which silently imposed the
		// time domain on every JSON service.
		rc.Schema = map[string]any{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type":    "object",
		}
	default: // "text"
		rc.ContentType = "text/plain"
		rc.Schema = map[string]any{"type": "string"}
	}
	return rc, nil
}

// Format returns the canonical generation/proof token ("json" | "text") derived
// from the ANCHORED ContentType. Codegen + proof must read this, never the raw
// response_format decision string — otherwise the artifact that gets built and
// proven can disagree with the ratified contract (e.g. raw "plain text" misses an
// exact `== "text"` check and silently generates JSON). The contract is the
// single source of format truth.
func (rc ResponseContract) Format() string {
	if rc.ContentType == "text/plain" {
		return "text"
	}
	return "json"
}

// normalizeResponseFormat maps a free-text format answer to a canonical token in
// the PROVABLE set ("json" | "text"), or returns an error. A human or LLM may
// phrase the same intent many ways ("json", "JSON", "application/json", "JSON
// format", "plain text", "text/plain") — these collapse. It NEVER silently
// defaults: unrecognized, ambiguous (more than one format named — e.g. "no json,
// xml only"), and not-yet-supported (xml) all fail loud, so a contract that
// contradicts the stated format can never be assembled. xml is detected only to
// reject it explicitly — the codegen+proof pipeline cannot yet produce/validate
// XML, so anchoring an application/xml contract would be an unprovable (worse,
// falsely-provable-against-JSON) spec.
func normalizeResponseFormat(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", fmt.Errorf("response_format is empty")
	}
	jsonM := strings.Contains(v, "json")
	xmlM := strings.Contains(v, "xml")
	textM := strings.Contains(v, "plain") || v == "text" || v == "text/plain"
	switch b2i(jsonM) + b2i(xmlM) + b2i(textM) {
	case 0:
		return "", fmt.Errorf("response_format %q is not a recognized format (use JSON or plain text)", strings.TrimSpace(raw))
	case 1:
		switch {
		case jsonM:
			return "json", nil
		case textM:
			return "text", nil
		default: // xml only — detected, explicitly rejected
			return "", fmt.Errorf("response_format %q (XML) is not yet supported by the proof pipeline; use JSON or plain text", strings.TrimSpace(raw))
		}
	default: // names more than one format — ambiguous, never guess
		return "", fmt.Errorf("response_format %q names more than one format; state exactly one (JSON or plain text)", strings.TrimSpace(raw))
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func buildDimensions(answers, kinds map[string]string, checklist []completeness.RequiredDecision) []Dimension {
	byDim := map[completeness.Dimension]*Dimension{}
	var order []completeness.Dimension
	for _, rd := range checklist {
		d, ok := byDim[rd.Dimension]
		if !ok {
			d = &Dimension{Name: rd.Dimension, Values: map[string]string{}, ValueKind: "precise"}
			byDim[rd.Dimension] = d
			order = append(order, rd.Dimension)
		}
		d.Values[rd.Key] = answers[rd.Key]
		// A dimension is fallback_preset if any of its decisions used a fallback.
		if kinds[rd.Key] == "fallback_preset" {
			d.ValueKind = "fallback_preset"
		}
	}
	out := make([]Dimension, 0, len(order))
	for _, name := range order {
		out = append(out, *byDim[name])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func cloneStrMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
