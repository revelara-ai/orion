package conductor

import (
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// teachCaseShape (or-4j37): error messages are load-bearing UI for the model
// on the other end — a rejection that doesn't teach converts a capable-enough
// model into a token furnace (the gemma dogfood run burned 131K tokens
// retry-looping on terse anchor rejections, then asked the human to bypass
// the proof structure entirely). So an add_requirement validation failure
// keeps the precise anchor diagnosis (what was wrong with THIS payload) and
// appends: the closed case union, a minimal valid example for the kind that
// failed, and — for unit/unknown kinds, where test-only intents end up — the
// steer to the brownfield change flow, whose regression gate proves "the new
// tests pass" without any structured case capture.
func teachCaseShape(err error, cases []spec.BehavioralCase) error {
	if err == nil {
		return nil
	}
	hasUnit, hasExec, hasFile, hasUnknown := false, false, false, false
	for _, c := range cases {
		switch c.Kind {
		case spec.KindUnit:
			hasUnit = true
		case spec.KindExec:
			hasExec = true
		case spec.KindFile:
			hasFile = true
		case spec.KindHTTP:
		default:
			hasUnknown = true
		}
	}
	var b strings.Builder
	b.WriteString("\n\nThe case model is a CLOSED UNION — kind ∈ {http (the default when kind is omitted), exec, unit, file}; a case must anchor mechanically or it cannot become a proof obligation.")
	switch {
	case hasUnit || hasUnknown:
		b.WriteString(`
Minimal VALID unit case:
  {"kind":"unit","unit":{"pkg":"storage","steps":[{"call":"Put(\"k\",\"v\")","want":"error(nil)"}]}}
Rules: call is a Go EXPRESSION over the package's exported surface (wrap multi-returns: func() error { _, err := F(); return err }()); each step sets EXACTLY ONE of want / want_err_re; want is a Go literal/expression.`)
	case hasExec:
		b.WriteString(`
Minimal VALID exec case:
  {"kind":"exec","exec":{"steps":[{"argv":["$BIN","--version"],"expect":{"exit":0,"stdout":[{"kind":"contains","value":"v"}]}}]}}
Rules: argv[0] must be $BIN; exactly one step; expectations are exit/stdout/stderr stream assertions.`)
	case hasFile:
		b.WriteString(`
Minimal VALID file case:
  {"kind":"file","file":{"assertions":[{"path":"README.md","kind":"exists"}]}}`)
	default:
		b.WriteString(`
Minimal VALID http case (kind omitted):
  {"request":{"method":"GET","path":"/time"},"expect":{"status":200,"content_type":"application/json","assertions":[{"kind":"json_key_present","key":"now"}]}}`)
	}
	if hasUnit || hasUnknown {
		b.WriteString(`
If the developer's intent is TEST-ONLY additions to an EXISTING repo, do not force it into cases at all: use submit_change_intent → build_change (the brownfield change flow, no cases needed) — the regression gate runs the new tests green-after, so "the tests pass" is proven without structured case capture.`)
	}
	return fmt.Errorf("%w%s", err, b.String())
}
