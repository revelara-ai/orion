package constraints

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/revelara-ai/orion/internal/architect"
)

// extractImplicitConstraints walks the source dir for one service and
// emits implicit constraints. v1 implementation supports Go only:
// context.WithTimeout calls become KindTimeoutBudget bindings.
//
// Other languages and other constraint kinds (KindRetryHygiene,
// KindIdempotencyKey) are TODO follow-ups; not blocking E1.
func extractImplicitConstraints(repoPath string, svc architect.Service) []ImplicitConstraint {
	if svc.Language != "go" && svc.Language != "" {
		// Skip non-Go services for v1.
		return nil
	}

	srcAbs := filepath.Join(repoPath, svc.SourceDir)
	info, err := os.Stat(srcAbs)
	if err != nil || !info.IsDir() {
		return nil
	}

	var out []ImplicitConstraint
	_ = filepath.WalkDir(srcAbs, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".go" {
			return nil
		}
		body, readErr := os.ReadFile(p) //nolint:gosec // G304/G122: p is under a constrained walk root
		if readErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(repoPath, p)
		out = append(out, scanGoForTimeouts(svc.Name, rel, body)...)
		return nil
	})
	return out
}

// goTimeoutRe matches `context.WithTimeout(... , <duration>)` patterns.
// Captures the duration expression (e.g., "5*time.Second", "30*time.Millisecond").
// Conservative: requires the literal "context.WithTimeout(" prefix.
var goTimeoutRe = regexp.MustCompile(
	`context\.WithTimeout\s*\(\s*[^,]+,\s*([^)]+)\)`,
)

// goDurationLiteralRe extracts a numeric value + unit from common Go
// duration literals: 5*time.Second, 30 * time.Millisecond, time.Second*2, etc.
// Captures: (1) leading number, (2) unit, (3) trailing number.
var goDurationLiteralRe = regexp.MustCompile(
	`(?:(\d+)\s*\*\s*)?time\.(Nanosecond|Microsecond|Millisecond|Second|Minute|Hour)(?:\s*\*\s*(\d+))?`,
)

func scanGoForTimeouts(svcName, relPath string, body []byte) []ImplicitConstraint {
	var out []ImplicitConstraint
	src := string(body)
	lines := strings.Split(src, "\n")

	// Walk line-by-line so we can attach a Line number.
	for lineIdx, line := range lines {
		matches := goTimeoutRe.FindAllStringSubmatchIndex(line, -1)
		for _, m := range matches {
			if len(m) < 4 {
				continue
			}
			durExpr := line[m[2]:m[3]]
			snippet := strings.TrimSpace(line)
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			out = append(out, ImplicitConstraint{
				Kind:        KindTimeoutBudget,
				Service:     svcName,
				SourceFile:  relPath,
				Line:        lineIdx + 1,
				ValueMillis: parseDurationToMillis(durExpr),
				RawSnippet:  snippet,
			})
		}
	}
	return out
}

// parseDurationToMillis attempts to convert a Go duration expression like
// "5 * time.Second" or "time.Millisecond * 30" into milliseconds.
// Returns 0 when the value is not a recognizable literal (e.g., variable).
func parseDurationToMillis(expr string) int {
	expr = strings.TrimSpace(expr)
	m := goDurationLiteralRe.FindStringSubmatch(expr)
	if m == nil {
		return 0
	}
	unit := m[2]
	num := 1
	if m[1] != "" {
		if n, err := strconv.Atoi(m[1]); err == nil {
			num = n
		}
	}
	if m[3] != "" {
		if n, err := strconv.Atoi(m[3]); err == nil {
			num *= n
		}
	}
	switch unit {
	case "Hour":
		return num * 3600 * 1000
	case "Minute":
		return num * 60 * 1000
	case "Second":
		return num * 1000
	case "Millisecond":
		return num
	case "Microsecond":
		return 0 // sub-millisecond rounds down
	case "Nanosecond":
		return 0
	}
	return 0
}
