package spec

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Contradiction is a pair of behavioral cases that demand incompatible responses
// for the SAME request — a spec that contains one cannot be implemented, so it
// must be resolved with the developer BEFORE it anchors (Manifesto: ambiguity
// resolved up front costs a conversation; discovered downstream it costs a
// rebuild). Detection is deterministic and conservative: only decidable
// conflicts are flagged, never heuristic ones.
type Contradiction struct {
	CaseA   string // content-addressed id of the first case
	CaseB   string // content-addressed id of the second case
	Request string // the shared request, rendered for the developer
	Reason  string // what the two cases disagree about
}

// FindContradictions groups cases by request identity and reports every
// decidable conflict within a group:
//   - two different statuses for one request
//   - two different (non-empty) content types for one request
//   - a raw-RFC3339 body demanded alongside any JSON-body assertion
//   - the same JSON key demanded in two different timezones
//
// Refinements are NOT conflicts: presence + format assertions on one key
// compose, and requests differing in query/headers/body are different requests.
func FindContradictions(cases []BehavioralCase) []Contradiction {
	groups := make(map[string][]BehavioralCase)
	var order []string
	for _, c := range cases {
		c.EnsureID()
		k := requestKey(c.Request)
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], c)
	}

	var out []Contradiction
	for _, k := range order {
		group := groups[k]
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				a, b := group[i], group[j]
				if reason := conflict(a.Expect, b.Expect); reason != "" {
					out = append(out, Contradiction{
						CaseA:   a.ID,
						CaseB:   b.ID,
						Request: renderRequest(a.Request),
						Reason:  reason,
					})
				}
			}
		}
	}
	return out
}

// conflict reports why two expectations for the same request are incompatible,
// or "" when they compose.
func conflict(a, b ExpectShape) string {
	if a.Status != b.Status {
		return fmt.Sprintf("one case requires status %d, the other %d", a.Status, b.Status)
	}
	if a.ContentType != "" && b.ContentType != "" && a.ContentType != b.ContentType {
		return fmt.Sprintf("one case requires content type %q, the other %q", a.ContentType, b.ContentType)
	}
	all := append(append([]BodyAssertion{}, a.Assertions...), b.Assertions...)
	rawBody, jsonBody := false, false
	zoneByKey := map[string]string{}
	for _, as := range all {
		switch as.Kind {
		case AssertBodyRFC3339:
			rawBody = true
		case AssertJSONKeyPresent, AssertJSONKeyRFC3339, AssertJSONKeyInTZ, AssertJSONErrorPresent:
			jsonBody = true
			if as.Kind == AssertJSONKeyInTZ {
				if prev, ok := zoneByKey[as.Key]; ok && prev != as.Value {
					return fmt.Sprintf("key %q is required in timezone %q by one case and %q by the other", as.Key, prev, as.Value)
				}
				zoneByKey[as.Key] = as.Value
			}
		}
	}
	if rawBody && jsonBody {
		return "one case requires a raw RFC3339 body, the other a JSON body — a response cannot be both"
	}
	return ""
}

// requestKey is the content-addressed identity of a request. encoding/json
// marshals maps with sorted keys, so equal requests always collide.
func requestKey(r RequestShape) string {
	b, _ := json.Marshal(r)
	return string(b)
}

func renderRequest(r RequestShape) string {
	var b strings.Builder
	b.WriteString(r.Method)
	b.WriteString(" ")
	b.WriteString(r.Path)
	if len(r.Query) > 0 {
		qb, _ := json.Marshal(r.Query)
		b.WriteString("?")
		b.Write(qb)
	}
	return b.String()
}
