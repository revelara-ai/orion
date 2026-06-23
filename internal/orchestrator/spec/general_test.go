package spec

import "testing"

// or-3ba.5 part 2: a non-HTTP project (CLI/library/worker) raises no HTTP
// response_format/route/port decisions, so those answers are absent. Compile must
// produce a minimal contract instead of erroring on the missing response_format.
func TestCompileNonHTTPSpecMinimalContract(t *testing.T) {
	es, err := Compile("a CLI tool that prints the date", map[string]string{}, map[string]string{}, nil, nil)
	if err != nil {
		t.Fatalf("non-HTTP spec should assemble + compile, got: %v", err)
	}
	rc := es.ResponseContract
	if rc.Route != "" || rc.ContentType != "" || rc.Port != 0 {
		t.Fatalf("non-HTTP contract should be minimal (no HTTP fields), got %+v", rc)
	}
	if len(rc.Cases) != 0 {
		t.Fatalf("non-HTTP spec with no requirements should synthesize no HTTP case, got %d", len(rc.Cases))
	}
	if es.Hash == "" {
		t.Fatal("compiled spec must be hashed/anchored")
	}
}

// Regression: an HTTP spec still gets its route/port/content-type + the synthesized
// happy-path default case.
func TestCompileHTTPSpecStillBuildsContract(t *testing.T) {
	answers := map[string]string{"response_format": "json", "timezone": "UTC", "port": "8080", "route": "/time"}
	es, err := Compile("an HTTP service", answers, map[string]string{}, nil, nil)
	if err != nil {
		t.Fatalf("HTTP spec should compile: %v", err)
	}
	rc := es.ResponseContract
	if rc.Route != "/time" || rc.Port != 8080 || rc.ContentType != "application/json" {
		t.Fatalf("HTTP contract wrong: %+v", rc)
	}
	if len(rc.Cases) != 1 {
		t.Fatalf("HTTP spec with no requirements should have exactly the 1 default case, got %d", len(rc.Cases))
	}
}
