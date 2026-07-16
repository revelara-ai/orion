package behavioral

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

// TestProverRegistry (or-4y7.5): Go is the default behavioral prover; an
// unregistered language resolves to nil (never a silent Go prover).
func TestProverRegistry(t *testing.T) {
	if proverFor("") == nil || proverFor("") != proverFor("go") || proverFor("go").Language() != "go" {
		t.Fatal(`proverFor("") must resolve to the go prover`)
	}
	if proverFor("python") != nil {
		t.Fatal("an unregistered language must resolve to nil")
	}
}

// TestProveRefusesUnregisteredLanguage (or-4y7.5): a contract whose language has
// no registered prover is REFUSED loudly — running the Go corpus over non-Go
// code would be a meaningless proof, and it must never happen silently.
func TestProveRefusesUnregisteredLanguage(t *testing.T) {
	_, err := Prove(context.Background(), t.TempDir(), testsynth.Contract{Language: "python"}, nil)
	if err == nil || !strings.Contains(err.Error(), "python") {
		t.Fatalf("an unregistered language must refuse naming it, got %v", err)
	}
}

// TestGoProverCorpusMatchesFreeFunctions (or-4y7.5): the Go prover's corpus is
// byte-identical to the V2.0 free functions it wraps.
func TestGoProverCorpusMatchesFreeFunctions(t *testing.T) {
	c := testsynth.Contract{Route: "/time", Format: "json"}
	files, err := proverFor("go").SynthesizeCorpus(c, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if files["orion_behavioral_test.go"] != testsynth.SynthesizeBehavioral(c) {
		t.Fatal("the go prover's root corpus must equal SynthesizeBehavioral verbatim")
	}
	for name, content := range testsynth.SynthesizeSupportFiles(c) {
		if files[name] != content {
			t.Fatalf("support file %s must ride the corpus verbatim", name)
		}
	}
}
