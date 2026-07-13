package spec

import "testing"

// TestBuildResponseContractFormats: the contract derivation collapses the many
// ways a human or LLM phrases a format to the right content type, and FAILS LOUD
// on an unrecognized value rather than silently anchoring text/plain (regression
// for the dogfooding defect: answer "application/json" → text/plain).
func TestBuildResponseContractFormats(t *testing.T) {
	base := func(format string) map[string]string {
		return map[string]string{"response_format": format, "timezone": "UTC", "port": "8080", "route": "/time"}
	}

	json := []string{"json", "JSON", "  json  ", "application/json", "JSON format", "json (application/json)", "application/json; charset=utf-8"}
	for _, v := range json {
		rc, err := buildResponseContract(base(v))
		if err != nil {
			t.Errorf("format %q: unexpected error %v", v, err)
			continue
		}
		if rc.ContentType != "application/json" {
			t.Errorf("format %q → content_type %q, want application/json", v, rc.ContentType)
		}
	}

	text := []string{"plain text", "Plain Text", "text", "text/plain", "plaintext"}
	for _, v := range text {
		rc, err := buildResponseContract(base(v))
		if err != nil || rc.ContentType != "text/plain" {
			t.Errorf("format %q → (%q, %v), want text/plain", v, rc.ContentType, err)
		}
	}

	// or-hbc: xml is now a first-class provable format.
	xmlF := []string{"xml", "XML", "application/xml"}
	for _, v := range xmlF {
		rc, err := buildResponseContract(base(v))
		if err != nil || rc.ContentType != "application/xml" {
			t.Errorf("format %q → (%q, %v), want application/xml", v, rc.ContentType, err)
		}
	}

	// Every value that is NOT cleanly one provable format must fail loud — never a
	// silent contract. Covers: unrecognized (csv/protobuf), ambiguous/negated
	// (more than one format named), and MIME types that merely contain "text"
	// (text/csv).
	failLoud := []string{
		"protobuf", "csv", "yaml", "binary", "gRPC", // unrecognized
		"no json please, xml only", "NOT json, use xml", // negation/conflict
		"a plain-text rendering of the xml", "json or xml", // ambiguous (two formats)
		"text/csv", "text/html", // contain "text" but aren't plain text
	}
	for _, v := range failLoud {
		if rc, err := buildResponseContract(base(v)); err == nil {
			t.Errorf("format %q: expected a fail-loud error, got content_type=%q (a silent/contradictory contract)", v, rc.ContentType)
		}
	}
}

// TestResponseContractFormatToken: the canonical Format() token (what codegen +
// proof read) is derived from the anchored ContentType — never the raw decision —
// so build/proof can't disagree with the ratified contract.
func TestResponseContractFormatToken(t *testing.T) {
	cases := []struct {
		answer string
		want   string
	}{
		{"json", "json"}, {"application/json", "json"}, {"JSON format", "json"},
		{"plain text", "text"}, {"text/plain", "text"}, {"text", "text"},
	}
	for _, c := range cases {
		rc, err := buildResponseContract(map[string]string{"response_format": c.answer, "timezone": "UTC", "port": "8080", "route": "/time"})
		if err != nil {
			t.Errorf("answer %q: %v", c.answer, err)
			continue
		}
		if rc.Format() != c.want {
			t.Errorf("answer %q → Format()=%q, want %q (content_type=%q)", c.answer, rc.Format(), c.want, rc.ContentType)
		}
	}
}
