package lang

import (
	"reflect"
	"testing"
)

// TestRegistryGoDefault (or-4y7.1): Go is the default — For("") and For("go")
// resolve to the same registered adapter; an unregistered language returns nil
// (never silently the Go adapter); Registered() is the sorted authority.
func TestRegistryGoDefault(t *testing.T) {
	if For("") == nil || For("") != For("go") {
		t.Fatal(`For("") must resolve to the Go default (== For("go"))`)
	}
	if For("go").Language() != "go" {
		t.Fatalf(`For("go").Language() = %q, want "go"`, For("go").Language())
	}
	if a := For("python"); a != nil {
		t.Fatalf("an unregistered language must return nil, not a silent Go adapter: %#v", a)
	}
	if got := Registered(); !reflect.DeepEqual(got, []string{"go"}) {
		t.Fatalf("Registered() = %v, want [go]", got)
	}
}
