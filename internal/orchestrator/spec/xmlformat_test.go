package spec

import "testing"

// or-hbc: xml is a first-class provable format — normalized, anchored as
// application/xml, and Format() round-trips it (never the json catch-all).
func TestXMLFormatRoundTrip(t *testing.T) {
	tok, err := normalizeResponseFormat("XML")
	if err != nil || tok != "xml" {
		t.Fatalf("xml must normalize: tok=%q err=%v", tok, err)
	}
	if _, err := normalizeResponseFormat("json or xml"); err == nil {
		t.Fatal("ambiguous formats must still fail loud")
	}
	rc, err := buildResponseContract(map[string]string{"response_format": "xml", "route": "/time", "port": "8080"})
	if err != nil {
		t.Fatalf("xml contract must assemble: %v", err)
	}
	if rc.ContentType != "application/xml" {
		t.Fatalf("anchored content-type = %q", rc.ContentType)
	}
	if rc.Format() != "xml" {
		t.Fatalf("Format() = %q — the JSON catch-all false-pass is back", rc.Format())
	}
}
