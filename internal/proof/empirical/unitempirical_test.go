package empirical

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

const persistentStorage = `package storage

import "os"

func Put(key, val string) error { return os.WriteFile(key+".kv", []byte(val), 0o644) }

func Get(key string) string {
	b, err := os.ReadFile(key + ".kv")
	if err != nil {
		return ""
	}
	return string(b)
}
`

const volatileStorage = `package storage

var mem = map[string]string{}

func Put(key, val string) error { mem[key] = val; return nil }
func Get(key string) string     { return mem[key] }
`

func writeApp(t *testing.T, storageSrc string) string {
	t.Helper()
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
	write("storage/storage.go", storageSrc)
	return dir
}

func r9Case() spec.BehavioralCase {
	c := spec.BehavioralCase{Kind: spec.KindUnit,
		ModesApply: []string{"empirical"}, ModesRationale: spec.RationaleCrossProcess,
		Unit: &spec.UnitCase{Pkg: "storage", Steps: []spec.UnitStep{
			{Call: `Put("k", "v")`, Want: `error(nil)`},
			{Call: `Get("k")`, Want: `"v"`, Restart: true},
		}}}
	c.EnsureID()
	return c
}

// TestUnitRestartProvesPersistence (or-v9f.23 R9): the restart segment is a
// REAL process boundary — file-backed storage survives it; in-memory storage
// passes segment 0 and fails segment 1. This distinction is exactly what an
// in-process test cannot express.
func TestUnitRestartProvesPersistence(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + executes a driver; skipped in -short")
	}
	cs := r9Case()

	mode, pr, err := Prove(context.Background(), writeApp(t, persistentStorage), testsynth.Contract{Cases: []spec.BehavioralCase{cs}})
	if err != nil {
		t.Fatal(err)
	}
	if ob := pr.Cases[cs.ID]; !ob.Executed || !ob.Passed {
		t.Fatalf("file-backed storage must survive the restart, got %+v (%s)", ob, mode.Output)
	}

	mode2, pr2, err := Prove(context.Background(), writeApp(t, volatileStorage), testsynth.Contract{Cases: []spec.BehavioralCase{cs}})
	if err != nil {
		t.Fatal(err)
	}
	ob := pr2.Cases[cs.ID]
	if !ob.Executed {
		t.Fatalf("the volatile case must still execute, got %+v (%s)", ob, mode2.Output)
	}
	if ob.Passed {
		t.Fatal("in-memory storage must NOT survive a process boundary — the restart is not real")
	}
}
