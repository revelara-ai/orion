package dedup

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// Scope is the suppression breadth of an annotation.
type Scope string

// Annotation scopes.
const (
	// ScopeStatement: the annotation suppresses the next single
	// statement (line-prior `// orion:ignore <pattern> reason="..."`).
	ScopeStatement Scope = "statement"

	// ScopeFile: the annotation suppresses one pattern across the
	// whole file (`// orion:ignore <pattern> file=true reason="..."`).
	ScopeFile Scope = "file"

	// ScopeAll: the annotation suppresses every pattern across the
	// whole file (`// orion:ignore-all reason="..."`).
	ScopeAll Scope = "all"
)

// Annotation is one parsed `// orion:ignore` annotation.
type Annotation struct {
	Pattern string // empty for ScopeAll
	Scope   Scope
	Reason  string
	Line    int // 1-indexed source line where the comment appears

	// AppliesToLine is the line number this annotation suppresses.
	// For ScopeStatement, this is the next non-blank, non-comment
	// line after the comment. For ScopeFile / ScopeAll it's
	// unused (the scope is the whole file).
	AppliesToLine int
}

// Warning is emitted for malformed annotations. The parser does NOT
// use a malformed annotation to suppress; over-detection is the
// safer failure mode per §8.3.
type Warning struct {
	Line    int
	Message string
}

// ParseFile walks a Go source file and returns all valid annotations
// plus warnings for any malformed ones encountered.
func ParseFile(content []byte) ([]Annotation, []Warning) {
	if len(content) == 0 {
		return nil, nil
	}
	lines := splitLines(content)
	var (
		annots   []Annotation
		warnings []Warning
	)
	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "//") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))
		switch {
		case strings.HasPrefix(body, "orion:ignore-all"):
			rest := strings.TrimSpace(strings.TrimPrefix(body, "orion:ignore-all"))
			if !strings.Contains(rest, "reason=") {
				warnings = append(warnings, Warning{Line: lineNum, Message: "malformed orion:ignore-all (missing reason=)"})
				continue
			}
			annots = append(annots, Annotation{
				Pattern: "",
				Scope:   ScopeAll,
				Reason:  extractReason(rest),
				Line:    lineNum,
			})
		case strings.HasPrefix(body, "orion:ignore"):
			rest := strings.TrimSpace(strings.TrimPrefix(body, "orion:ignore"))
			pattern := firstWord(rest)
			if pattern == "" || strings.HasPrefix(pattern, "reason=") || strings.HasPrefix(pattern, "file=") {
				warnings = append(warnings, Warning{Line: lineNum, Message: "malformed orion:ignore (missing pattern)"})
				continue
			}
			if !strings.Contains(rest, "reason=") {
				warnings = append(warnings, Warning{Line: lineNum, Message: fmt.Sprintf("malformed orion:ignore %s (missing reason=)", pattern)})
				continue
			}
			scope := ScopeStatement
			if hasFlag(rest, "file=true") {
				scope = ScopeFile
			}
			a := Annotation{
				Pattern: pattern,
				Scope:   scope,
				Reason:  extractReason(rest),
				Line:    lineNum,
			}
			if scope == ScopeStatement {
				a.AppliesToLine = nextNonBlankLine(lines, i+1)
			}
			annots = append(annots, a)
		}
	}
	return annots, warnings
}

// LookupAnnotation returns true if any annotation suppresses the
// given (lineNumber, pattern) pair.
func LookupAnnotation(annots []Annotation, lineNumber int, pattern string) bool {
	for _, a := range annots {
		switch a.Scope {
		case ScopeAll:
			return true
		case ScopeFile:
			if a.Pattern == pattern {
				return true
			}
		case ScopeStatement:
			if a.Pattern == pattern && a.AppliesToLine == lineNumber {
				return true
			}
		}
	}
	return false
}

// splitLines returns the file's lines preserving line numbers.
func splitLines(b []byte) []string {
	var out []string
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		out = append(out, scanner.Text())
	}
	return out
}

// firstWord returns the first whitespace-separated token of s.
func firstWord(s string) string {
	for i, r := range s {
		if r == ' ' || r == '\t' {
			return s[:i]
		}
	}
	return s
}

// hasFlag returns true if `flag` appears as a whitespace-separated
// token in s. Stricter than strings.Contains so e.g. `file=truthful`
// doesn't accidentally match.
func hasFlag(s, flag string) bool {
	for _, tok := range strings.Fields(s) {
		if tok == flag {
			return true
		}
	}
	return false
}

// extractReason pulls the `reason="..."` value out of the annotation
// body. v1 accepts double-quoted reasons; an unterminated quoted
// reason returns the rest of the string up to end of line.
func extractReason(s string) string {
	idx := strings.Index(s, "reason=")
	if idx < 0 {
		return ""
	}
	tail := s[idx+len("reason="):]
	if strings.HasPrefix(tail, "\"") {
		tail = tail[1:]
		end := strings.Index(tail, "\"")
		if end < 0 {
			return tail
		}
		return tail[:end]
	}
	return firstWord(tail)
}

// nextNonBlankLine returns the 1-indexed line number of the first
// non-blank, non-comment line at or after `start` (where start is
// 0-indexed into `lines`). Returns 0 when no such line exists.
func nextNonBlankLine(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "//") {
			continue
		}
		return i + 1
	}
	return 0
}
