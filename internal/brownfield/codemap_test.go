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
