package behavioral

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

// TestUnitCasesProveLibraryBehaviorally (or-v9f.23 R2): a multi-package
// artifact's library contract — ParseConfig names the missing field in its
// error — becomes an executed, in-package obligation.
func TestUnitCasesProveLibraryBehaviorally(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a corpus; skipped in -short")
	}
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module orion-generated/app\n\ngo 1.23\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	write("config/config.go", `package config

import "fmt"

type Config struct{ Name string }

func ParseConfig(raw string) (Config, error) {
	if raw == "" {
		return Config{}, fmt.Errorf("missing required field: name")
	}
	return Config{Name: raw}, nil
}
`)

	errCase := spec.BehavioralCase{Kind: spec.KindUnit, Unit: &spec.UnitCase{
		Pkg: "config",
		Steps: []spec.UnitStep{{
			Call:      `func() error { _, err := ParseConfig(""); return err }()`,
			WantErrRE: `missing required field: name`,
		}},
	}}
	valCase := spec.BehavioralCase{Kind: spec.KindUnit, Unit: &spec.UnitCase{
		Pkg: "config",
		Steps: []spec.UnitStep{{
			Call: `func() Config { c, _ := ParseConfig("orion"); return c }()`,
			Want: `Config{Name: "orion"}`,
		}},
	}}
	errCase.EnsureID()
	valCase.EnsureID()

	mr, err := Prove(context.Background(), dir, testsynth.Contract{Cases: []spec.BehavioralCase{errCase, valCase}, EntrySymbol: "run"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cs := range []spec.BehavioralCase{errCase, valCase} {
		ob := mr.Obligations[cs.ID]
		if !ob.Executed || !ob.Passed {
			t.Fatalf("unit obligation %s must execute and pass, got %+v (output:\n%s)", cs.ID, ob, mr.Output)
		}
	}

	// A library that stops naming the field fails the error-contract obligation.
	write("config/config.go", `package config

import "errors"

type Config struct{ Name string }

func ParseConfig(raw string) (Config, error) {
	if raw == "" {
		return Config{}, errors.New("bad input")
	}
	return Config{Name: raw}, nil
}
`)
	mr2, err := Prove(context.Background(), dir, testsynth.Contract{Cases: []spec.BehavioralCase{errCase}, EntrySymbol: "run"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ob := mr2.Obligations[errCase.ID]; ob.Passed {
		t.Fatalf("a drifted error contract must fail its obligation, got %+v", ob)
	}
}
