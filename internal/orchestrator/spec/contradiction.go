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
		k := stimulusKey(c)
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
				if reason := caseConflict(a, b); reason != "" {
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

// stimulusKey is the content-addressed identity of a case's STIMULUS (or-v9f.3):
// http cases keep the legacy request identity; exec cases group by {seed, argv,
// stdin, env} — expectations excluded, so two cases demanding different outcomes
// of one stimulus collide into a conflict check. encoding/json marshals maps
// with sorted keys, so equal stimuli always collide.
func stimulusKey(c BehavioralCase) string {
	if c.Kind == KindExec && c.Exec != nil && len(c.Exec.Steps) > 0 {
		st := c.Exec.Steps[0]
		b, _ := json.Marshal(struct {
			Seed  []FileSeed        `json:"seed,omitempty"`
			Argv  []string          `json:"argv"`
			Stdin string            `json:"stdin,omitempty"`
			Env   map[string]string `json:"env,omitempty"`
		}{c.Exec.Seed, st.Argv, st.Stdin, st.Env})
		return "exec:" + string(b)
	}
	b, _ := json.Marshal(c.Request)
	return "http:" + string(b)
}

// caseConflict dispatches the decidable-conflict check by kind. Cross-kind
// groups cannot form (stimulusKey namespaces by kind).
func caseConflict(a, b BehavioralCase) string {
	if a.Kind == KindExec {
		return execConflict(a.Exec.Steps[0].Expect, b.Exec.Steps[0].Expect)
	}
	return conflict(a.Expect, b.Expect)
}

// execConflict reports why two expectations of one exec stimulus are
// incompatible: different exit codes, or mutually exclusive stream demands
// (exact-vs-different-exact, exact-vs-empty). Regex-vs-regex is documented out
// of scope (composes or fails at proof time), same conservative posture as http.
func execConflict(a, b StepExpect) string {
	if a.Exit != nil && b.Exit != nil && *a.Exit != *b.Exit {
		return fmt.Sprintf("one case requires exit %d, the other %d", *a.Exit, *b.Exit)
	}
	for _, stream := range []struct {
		name string
		x, y []StreamAssertion
	}{{"stdout", a.Stdout, b.Stdout}, {"stderr", a.Stderr, b.Stderr}} {
		if reason := streamConflict(stream.name, append(append([]StreamAssertion{}, stream.x...), stream.y...)); reason != "" {
			return reason
		}
	}
	return ""
}

func streamConflict(name string, all []StreamAssertion) string {
	exact, empty := "", false
	haveExact := false
	for _, as := range all {
		switch as.Kind {
		case StreamExact:
			if haveExact && as.Value != exact {
				return fmt.Sprintf("%s is required to be exactly %q by one case and exactly %q by the other", name, exact, as.Value)
			}
			exact, haveExact = as.Value, true
		case StreamEmpty:
			empty = true
		}
	}
	if haveExact && empty && exact != "" {
		return fmt.Sprintf("%s is required to be exactly %q by one case and empty by the other", name, exact)
	}
	return ""
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
