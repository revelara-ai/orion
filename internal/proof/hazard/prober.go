package hazard

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// Prober is a language's hazard-source surface (or-4y7.7): it supplies the
// artifact's control-bearing SOURCE for the model-driven control-presence check.
// The STPA model + control-loop reasoning stay language-neutral; only reading the
// source varies. Resolved by stpa.Model.Language; the Go prober is the default.
type Prober interface {
	Language() string
	// SourceText returns the artifact's production source (for the token grep
	// that verifies a controlled UCA's control is present).
	SourceText(artifactDir string) string
	// ControlPresent reports whether a controlled UCA's declared tokens all
	// appear in the source (model-driven, not domain-hardcoded).
	ControlPresent(src string, u stpa.UCA) bool
}

var probers = map[string]Prober{}

func registerProber(p Prober) { probers[p.Language()] = p }

// proberFor resolves the hazard prober for a language ("" → go). An unregistered
// language returns nil (its registration accompanies lang.Registered()).
func proberFor(language string) Prober {
	if language == "" {
		language = "go"
	}
	return probers[language]
}

// goProber is the default: the V2.0 Go hazard source. Unlike the old single
// main.go read, it scans ALL production *.go — a control expressed in any package
// file (internal/server/…) is now seen, fixing a latent multi-file miss (the
// grep result is order-independent, so single-file artifacts are byte-identical).
type goProber struct{}

func (goProber) Language() string { return "go" }

func (goProber) SourceText(artifactDir string) string {
	var b strings.Builder
	_ = filepath.WalkDir(artifactDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			if data, rerr := os.ReadFile(p); rerr == nil {
				b.Write(data)
				b.WriteByte('\n')
			}
		}
		return nil
	})
	return b.String()
}

func (goProber) ControlPresent(src string, u stpa.UCA) bool { return controlPresent(src, u) }

func init() { registerProber(goProber{}) }

// pyProber is Python's hazard source (or-4y7.9): all production *.py (test and
// harness-authored orion_* files excluded), same order-independent token grep.
type pyProber struct{}

func (pyProber) Language() string { return "python" }

func (pyProber) SourceText(artifactDir string) string {
	var b strings.Builder
	_ = filepath.WalkDir(artifactDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || name == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".py") && !strings.HasPrefix(name, "test_") && !strings.HasPrefix(name, "orion_") {
			if data, rerr := os.ReadFile(p); rerr == nil {
				b.Write(data)
				b.WriteByte('\n')
			}
		}
		return nil
	})
	return b.String()
}

func (pyProber) ControlPresent(src string, u stpa.UCA) bool {
	return goProber{}.ControlPresent(src, u) // token grep is language-neutral
}

func init() { registerProber(pyProber{}) }
