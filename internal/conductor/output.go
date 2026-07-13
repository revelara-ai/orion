package conductor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// OutputRoot is the directory under which proven code is written into the
// developer's working tree, so accepted artifacts are visible in the repo they are
// working in (not buried in the context store). It is $ORION_OUTPUT_DIR when set,
// else <cwd>/orion-build. Returns "" only if the cwd cannot be resolved (export is
// then skipped — the code still lives in the build dir + the store).
func OutputRoot() string {
	if d := strings.TrimSpace(os.Getenv("ORION_OUTPUT_DIR")); d != "" {
		return d
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return filepath.Join(cwd, "orion-build")
}

// serviceSlug is a stable, filesystem-safe leaf name for a spec's output dir:
// route-derived for HTTP specs ("/time" → "time-service"); for a non-HTTP
// spec (no route) it derives from the INTENT instead — the route slug was
// meaningless there (or-045a.7).
func serviceSlug(es spec.ExecutableSpec) string {
	if strings.TrimSpace(es.ResponseContract.Route) == "" && strings.TrimSpace(es.Intent) != "" {
		return intentSlug(es.Intent)
	}
	r := strings.ToLower(strings.ReplaceAll(strings.Trim(es.ResponseContract.Route, "/"), "/", "-"))
	var b strings.Builder
	for _, c := range r {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "service"
	}
	return s + "-service"
}

// ServiceOutputDir is where a given spec's proven code is written, under root.
func ServiceOutputDir(root string, es spec.ExecutableSpec) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, serviceSlug(es))
}

// ExportProvenCode copies the generated source (go.mod + non-test .go files) from
// the build dir into destDir in the developer's repo, and writes an ORION.md
// provenance note. It EXCLUDES *_test.go so the harness-authored proof corpus is
// never exported (the build dir is already corpus-free; this is belt-and-suspenders
// for the trust wall). Returns the relative paths written, for reporting.
func ExportProvenCode(srcDir, destDir string, es spec.ExecutableSpec) ([]string, error) {
	if destDir == "" {
		return nil, fmt.Errorf("no output dir")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, fmt.Errorf("read build dir: %w", err)
	}
	var written []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, "_test.go") { // never export proof corpus
			continue
		}
		if name != "go.mod" && name != "go.sum" && !strings.HasSuffix(name, ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(destDir, name), data, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
		written = append(written, name)
	}
	if len(written) == 0 {
		return nil, fmt.Errorf("no source files in build dir %s", srcDir)
	}
	// or-nkcf: the generated investigation analyzer is the ONE subtree that
	// ships — the flat skip above deliberately excludes proof scratch dirs.
	if data, aerr := os.ReadFile(filepath.Join(srcDir, "cmd", "analyze", "main.go")); aerr == nil {
		anDest := filepath.Join(destDir, "cmd", "analyze")
		if err := os.MkdirAll(anDest, 0o755); err != nil {
			return nil, fmt.Errorf("create analyzer dir: %w", err)
		}
		if err := os.WriteFile(filepath.Join(anDest, "main.go"), data, 0o644); err != nil {
			return nil, fmt.Errorf("write analyzer: %w", err)
		}
		written = append(written, "cmd/analyze/main.go")
	}
	if err := os.WriteFile(filepath.Join(destDir, "ORION.md"), []byte(provenanceNote(es)), 0o644); err != nil {
		return nil, fmt.Errorf("write provenance: %w", err)
	}
	written = append(written, "ORION.md")
	sort.Strings(written)
	return written, nil
}

func provenanceNote(es spec.ExecutableSpec) string {
	var b strings.Builder
	b.WriteString("# Orion-generated service\n\n")
	b.WriteString("This code was generated and **independently proven** by Orion against a ratified spec\n(behavioral + empirical + hazard proof, all Accept).\n\n")
	fmt.Fprintf(&b, "- **Intent:** %s\n", strings.TrimSpace(es.Intent))
	fmt.Fprintf(&b, "- **Spec anchor:** `%s`\n", shortHash(es.Hash))
	// or-hn15.5: only an HTTP artifact has a route/port — a non-HTTP build must
	// not claim a phantom "/ · port 0" endpoint.
	if strings.TrimSpace(es.ResponseContract.Route) != "" {
		fmt.Fprintf(&b, "- **Route:** %s · **Port:** %d · **Format:** %s\n", es.ResponseContract.Route, es.ResponseContract.Port, es.ResponseContract.Format())
	}
	b.WriteString("\nEdit freely — re-running the build re-proves and overwrites the source here.\n")
	return b.String()
}

// locateProvenCode resolves the on-disk location of the current spec's proven code
// and lists the files present (for the conductor's "where is the code" answer).
func locateProvenCode(es spec.ExecutableSpec) (dir string, files []string, err error) {
	dir = ServiceOutputDir(OutputRoot(), es)
	if dir == "" {
		return "", nil, fmt.Errorf("output dir unresolved")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return dir, nil, err // not built yet / nothing there
	}
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return dir, files, nil
}

// intentSlug derives a stable filesystem-safe leaf from the intent's first
// words (a non-HTTP project has no route to slug).
func intentSlug(intent string) string {
	words := strings.Fields(strings.ToLower(intent))
	if len(words) > 4 {
		words = words[:4]
	}
	var b strings.Builder
	for i, w := range words {
		if i > 0 {
			b.WriteByte('-')
		}
		for _, c := range w {
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
				b.WriteRune(c)
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "project"
	}
	return s
}

// ExportProjectDocs writes the ratified project documents — the goals doc
// (with its anchor hash as provenance) and the hazard model — into destDir's
// docs/ folder (or-045a.7): the SUPPORTED surface for what the mech-game
// dogfood free-wrote into the harness cwd. Returns the relative paths written.
func ExportProjectDocs(destDir string, goals orchestrator.GoalsDoc, goalsHash string, model *stpa.Model) ([]string, error) {
	if destDir == "" {
		return nil, nil
	}
	docs := filepath.Join(destDir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		return nil, fmt.Errorf("export project docs: %w", err)
	}
	var written []string
	var g strings.Builder
	g.WriteString("# Project Goals\n\n")
	g.WriteString(goals.Render())
	fmt.Fprintf(&g, "\n\n---\nRatified in the Orion context store (anchor %s). Edit through Orion — this export is a provenance-stamped copy.\n", goalsHash)
	if err := os.WriteFile(filepath.Join(docs, "goals.md"), []byte(g.String()), 0o644); err != nil {
		return nil, err
	}
	written = append(written, "docs/goals.md")
	if model != nil {
		var h strings.Builder
		h.WriteString("# Hazard Analysis (STPA)\n\n## Losses\n")
		for _, l := range model.Losses {
			fmt.Fprintf(&h, "- %s: %s\n", l.ID, l.Description)
		}
		if len(model.Scenarios) > 0 {
			h.WriteString("\n## Loss scenarios\n")
			for _, sc := range model.Scenarios {
				fmt.Fprintf(&h, "- %s → %s: trigger %q, sustained by %q\n", sc.ID, sc.Loss, sc.Trigger, sc.SustainingCondition)
			}
		}
		h.WriteString("\n---\nRatified in the Orion context store. Edit through Orion.\n")
		if err := os.WriteFile(filepath.Join(docs, "hazards.md"), []byte(h.String()), 0o644); err != nil {
			return nil, err
		}
		written = append(written, "docs/hazards.md")
	}
	return written, nil
}
