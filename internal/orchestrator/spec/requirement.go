package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// AssertionKind is a checkable property of a response body (the proof domain knows
// how to execute each). The set is closed: an unknown kind cannot be proven, so it
// is rejected at compile (a requirement that can't become an executed obligation
// must fail, never silently pass — the or-y9d invariant).
type AssertionKind string

const (
	AssertJSONKeyPresent   AssertionKind = "json_key_present"   // body is JSON with Key present + non-empty
	AssertJSONKeyRFC3339   AssertionKind = "json_key_rfc3339"   // body[Key] parses as RFC3339
	AssertJSONKeyInTZ      AssertionKind = "json_key_in_tz"     // body[Key] is RFC3339 at the offset of Value (an IANA zone)
	AssertJSONErrorPresent AssertionKind = "json_error_present" // body is JSON with a non-empty "error" key
	AssertBodyRFC3339      AssertionKind = "body_rfc3339"       // raw body parses as RFC3339
)

var knownAssertionKinds = map[AssertionKind]bool{
	AssertJSONKeyPresent: true, AssertJSONKeyRFC3339: true, AssertJSONKeyInTZ: true,
	AssertJSONErrorPresent: true, AssertBodyRFC3339: true,
}

// BodyAssertion is one checkable property of a response body.
type BodyAssertion struct {
	Kind  AssertionKind `json:"kind"`
	Key   string        `json:"key,omitempty"`   // JSON key for json_key_* kinds
	Value string        `json:"value,omitempty"` // e.g. the IANA zone for json_key_in_tz
}

// RequestShape is the request a case issues.
type RequestShape struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   map[string]string `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// ExpectShape is the response a case requires.
type ExpectShape struct {
	Status      int             `json:"status"`
	ContentType string          `json:"content_type"`
	Assertions  []BodyAssertion `json:"assertions,omitempty"`
}

// BehavioralCase is the UNIT OF PROOF: a (request → expected response) the proof
// domain executes and reports executed+passed for. ID is content-addressed (stable
// across re-elicitation) and is the obligation key the gates match on.
type BehavioralCase struct {
	ID      string       `json:"id"`
	Request RequestShape `json:"request"`
	Expect  ExpectShape  `json:"expect"`
}

// Requirement is a stated behavior, lowered to >=1 BehavioralCase. Zero cases is a
// compile error (a requirement with nothing to execute can't be proven).
type Requirement struct {
	ID          string                 `json:"id"`
	Source      completeness.Dimension `json:"source"`
	DecisionKey string                 `json:"decision_key,omitempty"`
	Text        string                 `json:"text"`
	Cases       []BehavioralCase       `json:"cases"`
}

// caseID is the content-addressed id of a case (request+expect), 12 hex chars.
func caseID(c BehavioralCase) string {
	b, _ := json.Marshal(struct {
		R RequestShape `json:"r"`
		E ExpectShape  `json:"e"`
	}{c.Request, c.Expect})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:6])
}

// EnsureID sets a content-addressed ID if the case has none.
func (c *BehavioralCase) EnsureID() {
	if c.ID == "" {
		c.ID = caseID(*c)
	}
}

// requirementID is content-addressed over the normalized text + its cases.
func requirementID(r Requirement) string {
	cases := make([]BehavioralCase, len(r.Cases))
	copy(cases, r.Cases)
	for i := range cases {
		cases[i].EnsureID()
	}
	b, _ := json.Marshal(struct {
		T string           `json:"t"`
		C []BehavioralCase `json:"c"`
	}{strings.ToLower(strings.TrimSpace(r.Text)), cases})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:6])
}

// ValidateRequirement rejects a requirement that cannot be turned into executable
// obligations: zero cases, or a case the proof domain can't run (unknown assertion
// kind, unprovable content type, malformed timezone, missing fields).
func ValidateRequirement(r Requirement) error {
	if len(r.Cases) == 0 {
		return fmt.Errorf("requirement %q has no behavioral cases (nothing to prove)", r.Text)
	}
	for i := range r.Cases {
		if err := validateCase(r.Cases[i]); err != nil {
			return fmt.Errorf("case %d: %w", i, err)
		}
	}
	return nil
}

func validateCase(c BehavioralCase) error {
	if strings.TrimSpace(c.Request.Method) == "" {
		return fmt.Errorf("missing request method")
	}
	if strings.TrimSpace(c.Request.Path) == "" {
		return fmt.Errorf("missing request path")
	}
	if c.Expect.Status < 100 || c.Expect.Status > 599 {
		return fmt.Errorf("status %d is not a valid HTTP status", c.Expect.Status)
	}
	switch c.Expect.ContentType {
	case "application/json", "text/plain":
	default:
		return fmt.Errorf("content_type %q is not provable (use application/json or text/plain)", c.Expect.ContentType)
	}
	for _, a := range c.Expect.Assertions {
		if !knownAssertionKinds[a.Kind] {
			return fmt.Errorf("unknown assertion kind %q", a.Kind)
		}
		switch a.Kind {
		case AssertJSONKeyPresent, AssertJSONKeyRFC3339, AssertJSONKeyInTZ:
			if strings.TrimSpace(a.Key) == "" {
				return fmt.Errorf("assertion %s requires a json key", a.Kind)
			}
		}
		if a.Kind == AssertJSONKeyInTZ {
			if _, err := time.LoadLocation(a.Value); err != nil {
				return fmt.Errorf("assertion in_tz: %q is not a valid timezone", a.Value)
			}
		}
	}
	return nil
}

// SetIDs fills content-addressed IDs on a requirement and its cases (idempotent).
func (r *Requirement) SetIDs() {
	for i := range r.Cases {
		r.Cases[i].EnsureID()
	}
	r.ID = requirementID(*r)
}

// RequiredCaseIDs returns every case ID a contract's cases declare — the set the
// proof ObligationGate (Phase 3) requires to have executed and passed.
func (rc ResponseContract) RequiredCaseIDs() []string {
	out := make([]string, 0, len(rc.Cases))
	for _, c := range rc.Cases {
		out = append(out, c.ID)
	}
	return out
}
