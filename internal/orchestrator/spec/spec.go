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
// running artifact against. Derived from the approved functional decisions.
type ResponseContract struct {
	ContentType string         `json:"content_type"`
	Route       string         `json:"route"`
	Port        int            `json:"port"`
	TimeZone    string         `json:"timezone"`
	Schema      map[string]any `json:"schema"`
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
	Hash             string            `json:"hash"`
}

// Compile builds an ExecutableSpec from an intent and a complete set of answers.
// Every checklist decision must be present (callers apply fallback presets before
// compiling); a missing decision is a programming error, not a silent default.
// kinds maps a decision key to its value kind (precise|fallback_preset).
func Compile(intent string, answers map[string]string, kinds map[string]string, checklist []completeness.RequiredDecision) (ExecutableSpec, error) {
	for _, rd := range checklist {
		if strings.TrimSpace(answers[rd.Key]) == "" {
			return ExecutableSpec{}, fmt.Errorf("cannot compile spec: decision %q (%s) is unresolved", rd.Key, rd.Dimension)
		}
	}

	rc, err := buildResponseContract(answers)
	if err != nil {
		return ExecutableSpec{}, err
	}

	dims := buildDimensions(answers, kinds, checklist)

	s := ExecutableSpec{
		Intent:           strings.TrimSpace(intent),
		Decisions:        cloneStrMap(answers),
		Dimensions:       dims,
		ResponseContract: rc,
	}
	s.Hash = s.ComputeHash()
	return s, nil
}

// ComputeHash is the deterministic anchor over the spec content (excluding the
// hash field itself). The proof domain recomputes this on recall to verify the
// anchor.
func (s ExecutableSpec) ComputeHash() string {
	c := s
	c.Hash = ""
	b, _ := json.Marshal(c) // map keys marshal sorted; Dimensions pre-sorted
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// VerifyAnchor reports whether the spec's stored hash matches its content.
func (s ExecutableSpec) VerifyAnchor() bool {
	return s.Hash != "" && s.Hash == s.ComputeHash()
}

func buildResponseContract(a map[string]string) (ResponseContract, error) {
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
	switch strings.ToLower(strings.TrimSpace(a["response_format"])) {
	case "json":
		rc.ContentType = "application/json"
		rc.Schema = map[string]any{
			"$schema":              "https://json-schema.org/draft/2020-12/schema",
			"type":                 "object",
			"required":             []any{"time"},
			"additionalProperties": false,
			"properties": map[string]any{
				"time": map[string]any{"type": "string"},
			},
		}
	case "xml":
		rc.ContentType = "application/xml"
		rc.Schema = map[string]any{"type": "string"}
	default: // plain text and others
		rc.ContentType = "text/plain"
		rc.Schema = map[string]any{"type": "string"}
	}
	return rc, nil
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
