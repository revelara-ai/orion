package brownfield

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAt(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func has(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestScanRepoMapGoSurface: the map captures each Go package's EXPORTED API (not
// unexported helpers), its INTERNAL import edges (module-internal only; stdlib + 3rd
// party dropped), and ignores test files — the structural understanding the grill needs.
func TestScanRepoMapGoSurface(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, "go.mod", "module example.com/proj\n\ngo 1.25\n")
	writeAt(t, dir, "auth/auth.go", "package auth\n\nimport (\n\t\"errors\"\n\t\"example.com/proj/store\"\n)\n\nfunc Login(u string) error { if u == \"\" { return errors.New(\"x\") }; return store.Save(u) }\n\ntype Session struct{}\n\nfunc helper() {}\n")
	writeAt(t, dir, "store/store.go", "package store\n\nfunc Save(u string) error { return nil }\n\ntype DB struct{}\n")
	writeAt(t, dir, "auth/auth_test.go", "package auth\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n") // must be ignored

	m := ScanRepoMap(dir)
	if m.Module != "example.com/proj" {
		t.Fatalf("module = %q", m.Module)
	}
	if m.Profile.Mode != Brownfield {
		t.Fatalf("mode = %v", m.Profile.Mode)
	}
	if !has(m.KeyFiles, "go.mod") {
		t.Fatalf("key files missing go.mod: %v", m.KeyFiles)
	}

	var auth *GoPackage
	for i := range m.Packages {
		if m.Packages[i].Dir == "auth" {
			auth = &m.Packages[i]
		}
	}
	if auth == nil {
		t.Fatalf("auth package not mapped: %+v", m.Packages)
	}
	if !has(auth.Exported, "Login()") || !has(auth.Exported, "Session") {
		t.Fatalf("exported API surface wrong: %v", auth.Exported)
	}
	if has(auth.Exported, "helper()") {
		t.Fatalf("unexported helper leaked into the API surface: %v", auth.Exported)
	}
	if !has(auth.Imports, "store") {
		t.Fatalf("internal import edge auth→store missing: %v", auth.Imports)
	}
	if has(auth.Imports, "errors") {
		t.Fatalf("stdlib import should be dropped from the architecture edges: %v", auth.Imports)
	}

	d := m.Digest()
	for _, want := range []string{"Codebase map", "example.com/proj", "auth", "Login()", "store"} {
		if !strings.Contains(d, want) {
			t.Fatalf("digest missing %q:\n%s", want, d)
		}
	}
}

// TestRepoMapImpact: the reverse import graph drives impact analysis — a package
// imported by others has a blast radius; an importer with no dependents is an entry
// point; foundations rank by reach. This is what directs where an intent's work lands.
func TestRepoMapImpact(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, "go.mod", "module example.com/proj\n\ngo 1.25\n")
	// gui → service → store  (store is the foundation; gui is the entry point)
	writeAt(t, dir, "store/store.go", "package store\n\nfunc Save(s string) error { return nil }\n")
	writeAt(t, dir, "service/service.go", "package service\n\nimport \"example.com/proj/store\"\n\nfunc Do(s string) error { return store.Save(s) }\n")
	writeAt(t, dir, "gui/gui.go", "package gui\n\nimport \"example.com/proj/service\"\n\nfunc Run() error { return service.Do(\"x\") }\n")

	m := ScanRepoMap(dir)
	pkg := func(d string) *GoPackage {
		for i := range m.Packages {
			if m.Packages[i].Dir == d {
				return &m.Packages[i]
			}
		}
		return nil
	}
	// reverse edges
	if s := pkg("store"); s == nil || !has(s.Dependents, "service") {
		t.Fatalf("store should be depended on by service: %+v", s)
	}
	// transitive blast radius: changing store affects service AND gui
	br := m.BlastRadius("store")
	if !has(br, "service") || !has(br, "gui") {
		t.Fatalf("store blast radius should reach service + gui (transitive): %v", br)
	}
	// gui has no dependents → entry point
	var entries []string
	for _, p := range m.EntryPoints() {
		entries = append(entries, p.Dir)
	}
	if !has(entries, "gui") || has(entries, "store") {
		t.Fatalf("gui is an entry point, store is not: %v", entries)
	}
	// foundations rank store (reach 2) ahead of gui (reach 0)
	found := m.Foundations(3)
	if len(found) == 0 || found[0].Dir != "store" {
		t.Fatalf("store should be the top foundation: %+v", found)
	}
	if d := m.Digest(); !strings.Contains(d, "Architecture (impact)") || !strings.Contains(d, "blast radius") {
		t.Fatalf("digest should surface the impact section:\n%s", d)
	}
}

// TestScanRepoMapGreenfield: an empty/config-only workspace maps as greenfield with no
// packages — there's no existing structure to integrate with.
func TestScanRepoMapGreenfield(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, "README.md", "# new project")
	m := ScanRepoMap(dir)
	if m.Profile.Mode != Greenfield || len(m.Packages) != 0 {
		t.Fatalf("greenfield workspace should map with no packages: %+v", m)
	}
}
