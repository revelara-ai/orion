package brownfield

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RepoMap is a DETERMINISTIC structural understanding of a brownfield repo — the
// "preliminary understanding" the grill reads to ask informed questions and ground a
// spec/change in the EXISTING code (rather than inventing structure). It mirrors the
// deterministic half of a codebase-knowledge tool (inventory + import map + API
// surface); semantic enrichment (domains/flows, LLM- or knowledge-graph-derived) is a
// later layer built on this. No LLM, no compile — go/parser reads syntax only, so it
// works even on a repo that doesn't currently build.
type RepoMap struct {
	Profile   RepoProfile // greenfield/brownfield + languages + counts
	Module    string      // Go module path (from go.mod), if any
	KeyFiles  []string    // README, go.mod, package.json, Makefile, … (rel paths)
	Packages  []GoPackage // Go API surface + internal import graph (when Go)
	Truncated int         // packages omitted from Packages when the repo is large
}

// GoPackage is one Go package's public surface + its internal dependencies.
type GoPackage struct {
	Name       string   // package name
	Dir        string   // dir relative to the repo root
	Imports    []string // internal imports (within the module) — the architecture edges (out)
	Exported   []string // exported funcs + types (the API surface)
	Dependents []string // internal packages that import THIS one — the reverse edges (in)
}

const maxMappedPackages = 60 // bound the digest on very large repos

// ScanRepoMap builds the structural map of repoDir. Deterministic + cheap.
func ScanRepoMap(repoDir string) RepoMap {
	m := RepoMap{Profile: Classify(repoDir), Module: modulePath(repoDir)}
	m.KeyFiles = keyFiles(repoDir)
	for _, l := range m.Profile.Languages {
		if l == "go" {
			m.Packages, m.Truncated = scanGoPackages(repoDir, m.Module)
			computeDependents(m.Packages)
			break
		}
	}
	return m
}

// computeDependents fills each package's reverse edges (who imports it) from the
// forward import edges — the basis for blast-radius/impact analysis.
func computeDependents(pkgs []GoPackage) {
	idx := make(map[string]int, len(pkgs))
	for i, p := range pkgs {
		idx[p.Dir] = i
	}
	for _, p := range pkgs {
		for _, imp := range p.Imports {
			if j, ok := idx[imp]; ok {
				pkgs[j].Dependents = append(pkgs[j].Dependents, p.Dir)
			}
		}
	}
	for i := range pkgs {
		sort.Strings(pkgs[i].Dependents)
	}
}

// BlastRadius returns the transitive set of packages affected by a change to dir
// (everything that depends on it, directly or indirectly) — the impact of touching it.
func (m RepoMap) BlastRadius(dir string) []string {
	rev := make(map[string][]string, len(m.Packages))
	for _, p := range m.Packages {
		rev[p.Dir] = p.Dependents
	}
	seen := map[string]bool{}
	queue := append([]string(nil), rev[dir]...)
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		if seen[d] {
			continue
		}
		seen[d] = true
		queue = append(queue, rev[d]...)
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// Foundations returns up to n packages with the widest blast radius (high fan-in) —
// the risky-to-change core: a change here ripples across the system.
func (m RepoMap) Foundations(n int) []GoPackage {
	type fr struct {
		p     GoPackage
		reach int
	}
	frs := make([]fr, 0, len(m.Packages))
	for _, p := range m.Packages {
		frs = append(frs, fr{p, len(m.BlastRadius(p.Dir))})
	}
	sort.Slice(frs, func(i, j int) bool {
		if frs[i].reach != frs[j].reach {
			return frs[i].reach > frs[j].reach
		}
		return frs[i].p.Dir < frs[j].p.Dir
	})
	var out []GoPackage
	for _, f := range frs {
		if f.reach == 0 || len(out) >= n {
			break
		}
		out = append(out, f.p)
	}
	return out
}

// EntryPoints returns packages nothing else imports — the roots (mains/top-level): a
// change here is localized (no internal dependents).
func (m RepoMap) EntryPoints() []GoPackage {
	var out []GoPackage
	for _, p := range m.Packages {
		if len(p.Dependents) == 0 {
			out = append(out, p)
		}
	}
	return out
}

// modulePath reads the `module` line from go.mod, or "" if absent.
func modulePath(repoDir string) string {
	data, err := os.ReadFile(filepath.Join(repoDir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(line); strings.HasPrefix(s, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(s, "module "))
		}
	}
	return ""
}

// keyFiles lists notable root-level files that orient a reader.
func keyFiles(repoDir string) []string {
	var out []string
	for _, n := range []string{"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "Makefile", "README.md", "README", "CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(repoDir, n)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// scanGoPackages parses every non-test .go file (syntax only) grouped by directory,
// extracting each package's exported API + its internal imports (under modPath — the
// architecture edges; stdlib/3rd-party imports are dropped as noise).
func scanGoPackages(repoDir, modPath string) ([]GoPackage, int) {
	type acc struct {
		name     string
		imports  map[string]bool
		exported map[string]bool
	}
	byDir := map[string]*acc{}
	fset := token.NewFileSet()

	_ = filepath.WalkDir(repoDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil // unparseable file: skip, don't fail the whole scan
		}
		rel, _ := filepath.Rel(repoDir, filepath.Dir(path))
		a := byDir[rel]
		if a == nil {
			a = &acc{imports: map[string]bool{}, exported: map[string]bool{}}
			byDir[rel] = a
		}
		a.name = f.Name.Name
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if modPath != "" && (p == modPath || strings.HasPrefix(p, modPath+"/")) {
				rel := strings.TrimPrefix(strings.TrimPrefix(p, modPath), "/")
				if rel == "" {
					rel = "." // an import of the module root package
				}
				a.imports[rel] = true
			}
		}
		for _, decl := range f.Decls {
			switch dd := decl.(type) {
			case *ast.FuncDecl:
				if dd.Recv == nil && dd.Name.IsExported() { // top-level exported func
					a.exported[dd.Name.Name+"()"] = true
				}
			case *ast.GenDecl:
				for _, spec := range dd.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
						a.exported[ts.Name.Name] = true
					}
				}
			}
		}
		return nil
	})

	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	var pkgs []GoPackage
	truncated := 0
	for _, dir := range dirs {
		if len(pkgs) >= maxMappedPackages {
			truncated = len(dirs) - len(pkgs)
			break
		}
		a := byDir[dir]
		pkgs = append(pkgs, GoPackage{Name: a.name, Dir: dir, Imports: sortedKeys(a.imports), Exported: sortedKeys(a.exported)})
	}
	return pkgs, truncated
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Digest renders the map as a compact digest for the grill to read.
func (m RepoMap) Digest() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Codebase map (%s)\n", m.Profile.Mode)
	if len(m.Profile.Languages) > 0 {
		fmt.Fprintf(&b, "languages: %s · source files: %d · tests: %v\n", strings.Join(m.Profile.Languages, ", "), m.Profile.SourceFiles, m.Profile.HasTests)
	}
	if m.Module != "" {
		fmt.Fprintf(&b, "go module: %s\n", m.Module)
	}
	if len(m.KeyFiles) > 0 {
		fmt.Fprintf(&b, "key files: %s\n", strings.Join(m.KeyFiles, ", "))
	}
	// Architecture/impact: foundations (wide blast radius — risky) + entry points
	// (localized). This is what directs where an intent's work lands + its risk.
	if found := m.Foundations(6); len(found) > 0 {
		b.WriteString("\n## Architecture (impact)\n")
		b.WriteString("foundations (high blast radius — change with care):\n")
		for _, p := range found {
			fmt.Fprintf(&b, "- %s → affects %d package(s)\n", p.Dir, len(m.BlastRadius(p.Dir)))
		}
		eps := m.EntryPoints()
		dirs := make([]string, 0, len(eps))
		for _, p := range eps {
			dirs = append(dirs, p.Dir)
		}
		if len(dirs) > 0 {
			fmt.Fprintf(&b, "entry points (roots — localized to change): %s\n", strings.Join(clipList(dirs, 12), ", "))
		}
	}

	if len(m.Packages) > 0 {
		fmt.Fprintf(&b, "\n## Go packages (%d) — API surface + internal deps\n", len(m.Packages))
		for _, p := range m.Packages {
			fmt.Fprintf(&b, "\n### %s  (package %s)\n", p.Dir, p.Name)
			if len(p.Imports) > 0 {
				fmt.Fprintf(&b, "imports: %s\n", strings.Join(clipList(p.Imports, 12), ", "))
			}
			if len(p.Exported) > 0 {
				fmt.Fprintf(&b, "exports: %s\n", strings.Join(clipList(p.Exported, 20), ", "))
			}
		}
		if m.Truncated > 0 {
			fmt.Fprintf(&b, "\n… (%d more packages omitted)\n", m.Truncated)
		}
	}
	return b.String()
}

func clipList(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(xs[:n:n], fmt.Sprintf("…+%d", len(xs)-n))
}
