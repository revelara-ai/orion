package brownfield

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/safeenv"
)

// Mode classifies a target repo: greenfield (Orion is creating new file structure)
// vs brownfield (Orion is integrating with existing code). This is the step-2 fork
// that determines create-vs-edit everywhere downstream (generation makes new files vs
// surgical diffs; the spec is created vs edited; the proof adds vs preserves+adds).
type Mode string

const (
	Greenfield Mode = "greenfield"
	Brownfield Mode = "brownfield"
)

// RepoProfile is what Orion understands about a target repo before it starts: the
// greenfield/brownfield mode and the signals behind it.
type RepoProfile struct {
	Mode        Mode
	HasGit      bool
	HasCommits  bool
	SourceFiles int
	Languages   []string
	HasTests    bool // a regression baseline is possible (existing tests)
}

// Classify inspects repoDir and decides greenfield vs brownfield: BROWNFIELD when
// there is existing source to integrate with (recognized source files) OR an existing
// git history; GREENFIELD otherwise (empty/new, config-only, no commits). Orion's own
// build output (orion-build/) and the usual non-source dirs are ignored, so a fresh
// repo that has only run a build still reads greenfield.
func Classify(repoDir string) RepoProfile {
	p := RepoProfile{Mode: Greenfield}

	if isDir(filepath.Join(repoDir, ".git")) {
		p.HasGit = true
		cmd := exec.Command("git", "rev-list", "-n", "1", "--all")
		cmd.Dir = repoDir
		cmd.Env = safeenv.Build() // never inherit host secrets when shelling out in a target repo
		if out, err := cmd.Output(); err == nil && strings.TrimSpace(string(out)) != "" {
			p.HasCommits = true
		}
	}

	langs := map[string]bool{}
	_ = filepath.WalkDir(repoDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if skipDir(name) {
				return fs.SkipDir
			}
			return nil
		}
		if lang, ok := sourceLang(name); ok {
			p.SourceFiles++
			langs[lang] = true
			if isTestFile(name) {
				p.HasTests = true
			}
		}
		return nil
	})
	for l := range langs {
		p.Languages = append(p.Languages, l)
	}
	sort.Strings(p.Languages)

	if p.SourceFiles > 0 || p.HasCommits {
		p.Mode = Brownfield
	}
	return p
}

func skipDir(name string) bool {
	switch name {
	case ".git", "orion-build", "node_modules", "vendor", "target", "dist", "build", ".orion":
		return true
	}
	return strings.HasPrefix(name, ".") // hidden dirs (.idea, .vscode, …)
}

// sourceLang maps a filename to a source language (the developer's code, not config).
func sourceLang(name string) (string, bool) {
	switch {
	case strings.HasSuffix(name, ".go"):
		return "go", true
	case strings.HasSuffix(name, ".ts"), strings.HasSuffix(name, ".tsx"):
		return "typescript", true
	case strings.HasSuffix(name, ".js"), strings.HasSuffix(name, ".jsx"), strings.HasSuffix(name, ".mjs"):
		return "javascript", true
	case strings.HasSuffix(name, ".py"):
		return "python", true
	case strings.HasSuffix(name, ".rs"):
		return "rust", true
	case strings.HasSuffix(name, ".java"):
		return "java", true
	case strings.HasSuffix(name, ".rb"):
		return "ruby", true
	case strings.HasSuffix(name, ".c"), strings.HasSuffix(name, ".cc"), strings.HasSuffix(name, ".cpp"), strings.HasSuffix(name, ".h"), strings.HasSuffix(name, ".hpp"):
		return "c/c++", true
	}
	return "", false
}

func isTestFile(name string) bool {
	return strings.HasSuffix(name, "_test.go") ||
		strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") ||
		strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py") ||
		strings.HasSuffix(name, "Test.java")
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
