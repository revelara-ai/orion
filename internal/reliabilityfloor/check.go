package reliabilityfloor

import "strings"

// concernLinters maps a reliability concern (matched as a lowercase substring of a
// signal's Title+Why) to the golangci-lint v2 linters that mechanize it. Curated and
// low-false-positive by design; unmatched signals stay advisory (Check.Kind == none).
var concernLinters = []struct {
	keywords []string
	linters  []string
}{
	{[]string{"timeout", "without context", "no context", "deadline"}, []string{"noctx", "contextcheck"}},
	{[]string{"body", "unclosed", "leak", "resource not closed"}, []string{"bodyclose"}},
	{[]string{"sql rows", "rows.err", "sql result"}, []string{"rowserrcheck", "sqlclosecheck"}},
	{[]string{"swallow", "ignored error", "unchecked error"}, []string{"errcheck"}},
	{[]string{"injection", "insecure", "hardcoded credential", "weak crypto"}, []string{"gosec"}},
}

// AttachChecks sets Check on each signal by matching concern keywords; unmatched → none.
func AttachChecks(sigs []Signal) []Signal {
	out := make([]Signal, len(sigs))
	for i, s := range sigs {
		hay := strings.ToLower(s.Title + " " + s.Why)
		s.Check = Check{Kind: CheckNone}
		for _, m := range concernLinters {
			if anySubstr(hay, m.keywords) {
				s.Check = Check{Kind: CheckGolangciLint, Linters: m.linters}
				break
			}
		}
		out[i] = s
	}
	return out
}

func anySubstr(hay string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(hay, n) {
			return true
		}
	}
	return false
}
