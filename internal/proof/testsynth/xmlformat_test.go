package testsynth

import (
	"strings"
	"testing"
)

// or-hbc: the behavioral corpus for an XML contract asserts application/xml
// content-type + well-formedness — never the JSON catch-all (the false-pass
// the adversarial review found).
func TestXMLContractCorpus(t *testing.T) {
	c := Contract{Route: "/time", Format: "xml"}
	src := SynthesizeBehavioral(c)
	for _, must := range []string{"application/xml", "xml.Unmarshal", "well-formed XML"} {
		if !strings.Contains(src, must) {
			t.Fatalf("xml corpus missing %q:\n%s", must, src)
		}
	}
	if strings.Contains(src, `want application/json`) {
		t.Fatalf("xml corpus fell through to the JSON catch-all:\n%s", src)
	}
}
