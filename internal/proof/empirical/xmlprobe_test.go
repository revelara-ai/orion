package empirical

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

// or-hbc: the empirical channel judges an XML contract by application/xml
// content-type + well-formedness — a JSON artifact FAILS the xml contract
// (the false-pass the bead closes), and a real XML artifact passes.
func TestXMLProbeContract(t *testing.T) {
	xmlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, "<time>%s</time>", time.Now().UTC().Format(time.RFC3339))
	}))
	defer xmlSrv.Close()
	pr := probeContract(xmlSrv.Listener.Addr().String(), testsynth.Contract{Route: "/", Format: "xml"})
	if !pr.PortOpen || !pr.ResponseContractSatisfied {
		t.Fatalf("well-formed XML artifact must satisfy the xml contract: %+v", pr)
	}

	jsonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"time":"2026-01-01T00:00:00Z"}`)
	}))
	defer jsonSrv.Close()
	pr = probeContract(jsonSrv.Listener.Addr().String(), testsynth.Contract{Route: "/", Format: "xml"})
	if pr.ResponseContractSatisfied {
		t.Fatal("a JSON artifact must FAIL an xml contract — the false-pass is the bug this closes")
	}

	malformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, "<time>unclosed")
	}))
	defer malformed.Close()
	pr = probeContract(malformed.Listener.Addr().String(), testsynth.Contract{Route: "/", Format: "xml"})
	if pr.ResponseContractSatisfied {
		t.Fatal("malformed XML must fail well-formedness")
	}
}
