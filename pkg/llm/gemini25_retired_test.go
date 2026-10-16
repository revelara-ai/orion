package llm

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// retired25 is assembled so this file does not trip its own guard.
const retired25 = "gemini-2" + ".5"

// po-s1x5k.10: Google blocks Gemini 2.5 for inactive projects from
// 2026-10-20 and shuts the family down by 2027-03-31. Orion prices live
// calls only, so the 2.5 rows are gone: a 2.5 call books UNPRICED, loudly.
func TestGemini25NotPriced(t *testing.T) {
	for _, m := range []string{retired25 + "-pro", retired25 + "-flash", retired25 + "-flash-lite"} {
		if p, ok := PriceFor("gemini", m); ok {
			t.Errorf("%s is priced (%+v); the 2.5 rows must stay removed", m, p)
		}
	}
}

// TestGemini25OnlyInDatedNotes: a 2.5 id in an example or a fixture is
// copied into a config that stops answering. Each line that names one must
// carry the ISO date that makes it a dated historical note.
func TestGemini25OnlyInDatedNotes(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root not found at %s: %v", root, err)
	}
	dated := regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); path != root && (strings.HasPrefix(n, ".") || n == "vendor" || n == "node_modules" || n == "graphify-out") {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".md", ".yaml", ".yml", ".json", ".toml":
		default:
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for n := 1; sc.Scan(); n++ {
			if line := sc.Text(); strings.Contains(strings.ToLower(line), retired25) && !dated.MatchString(line) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d names %s outside a dated historical note", rel, n, retired25)
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}
