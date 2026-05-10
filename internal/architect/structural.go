package architect

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// annotateServiceLanguages tries to set Service.Language and SourceDir
// based on directory layout under src/. Best-effort and language-list
// is bounded; unknown languages are left empty.
//
// The microservices-demo convention is src/<servicename>/ with the
// service implemented inside. We use that as the primary signal.
func annotateServiceLanguages(repoPath string, model *ArchitecturalModel) error {
	srcDir := filepath.Join(repoPath, "src")
	info, err := os.Stat(srcDir)
	if err != nil || !info.IsDir() {
		return nil
	}

	// Build a map of subdirectory name -> primary language for fast lookup.
	subdirLang := map[string]string{}
	subdirPath := map[string]string{}
	entries, _ := os.ReadDir(srcDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(srcDir, e.Name())
		lang := detectPrimaryLanguage(full)
		subdirLang[e.Name()] = lang
		subdirPath[e.Name()] = filepath.Join("src", e.Name())
	}

	// For each service in the model, find a subdir whose name contains
	// the service's name (case-insensitive). Conservative match to avoid
	// false positives.
	for i := range model.Services {
		want := strings.ToLower(model.Services[i].Name)
		for sub, lang := range subdirLang {
			lo := strings.ToLower(sub)
			if lo == want || strings.Contains(lo, want) || strings.Contains(want, lo) {
				model.Services[i].Language = lang
				model.Services[i].SourceDir = subdirPath[sub]
				break
			}
		}
	}
	return nil
}

// detectPrimaryLanguage returns a coarse language label for a directory by
// counting source-file extensions. Bounded to languages microservices-
// demo uses and orion's pattern set targets in v1.x.
func detectPrimaryLanguage(dir string) string {
	counts := map[string]int{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		switch ext {
		case ".go":
			counts["go"]++
		case ".py":
			counts["python"]++
		case ".java", ".kt":
			counts["java"]++
		case ".cs":
			counts["csharp"]++
		case ".js", ".mjs", ".cjs", ".ts":
			counts["javascript"]++
		case ".rb":
			counts["ruby"]++
		case ".rs":
			counts["rust"]++
		}
		return nil
	})
	if len(counts) == 0 {
		return ""
	}
	var best string
	var bestN int
	for lang, n := range counts {
		if n > bestN || (n == bestN && lang < best) {
			best, bestN = lang, n
		}
	}
	return best
}
