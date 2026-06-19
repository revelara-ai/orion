package proof

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// secretRe matches common hardcoded-credential shapes. Deterministic; the
// secret-scan is a proof gate (PRD Security Requirements): a hardcoded credential
// blocks the deployment bar — secrets never ship.
var secretRe = regexp.MustCompile(`(?i)(password|passwd|secret|api[_-]?key|access[_-]?key|token|private[_-]?key|aws_secret)\s*[:=]\s*["'][^"']{6,}["']`)

// SecretScan returns the source locations that look like hardcoded secrets across
// the artifact's Go files.
func SecretScan(artifactDir string) []string {
	var findings []string
	_ = filepath.WalkDir(artifactDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		for i, line := range strings.Split(string(b), "\n") {
			if secretRe.MatchString(line) {
				rel, _ := filepath.Rel(artifactDir, path)
				findings = append(findings, rel+":"+itoa(i+1))
			}
		}
		return nil
	})
	return findings
}

// SecurityClean reports whether the artifact has no hardcoded secrets.
func SecurityClean(artifactDir string) bool { return len(SecretScan(artifactDir)) == 0 }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
