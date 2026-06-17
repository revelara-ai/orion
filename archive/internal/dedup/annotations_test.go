package dedup

import (
	"strings"
	"testing"
)

func TestAnnotations_LineForm(t *testing.T) {
	src := `package x

func F() {
    // orion:ignore missing_timeout reason="legacy callsite"
    callTheThing()
}
`
	annots, warnings := ParseFile([]byte(src))
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(annots) != 1 {
		t.Fatalf("got %d annotations, want 1", len(annots))
	}
	a := annots[0]
	if a.Pattern != "missing_timeout" || a.Scope != ScopeStatement {
		t.Errorf("got %+v", a)
	}
	// The annotation is on line 4 (1-indexed); it suppresses the
	// next statement on line 5.
	if !LookupAnnotation(annots, 5, "missing_timeout") {
		t.Error("LookupAnnotation should return true for line 5 + matching pattern")
	}
	// Doesn't suppress a different pattern.
	if LookupAnnotation(annots, 5, "rate_limit_inference") {
		t.Error("line-form annotation should not suppress different pattern")
	}
	// Doesn't suppress an unrelated line.
	if LookupAnnotation(annots, 7, "missing_timeout") {
		t.Error("line-form annotation should not suppress unrelated lines")
	}
}

func TestAnnotations_FileScopedPattern(t *testing.T) {
	src := `// orion:ignore missing_timeout file=true reason="legacy module"
package x

func F() {
    callA()
    callB()
}
`
	annots, warnings := ParseFile([]byte(src))
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(annots) != 1 {
		t.Fatalf("got %d annotations, want 1", len(annots))
	}
	if annots[0].Scope != ScopeFile || annots[0].Pattern != "missing_timeout" {
		t.Errorf("got %+v", annots[0])
	}
	// File-scoped suppresses ANY line in the file for the matched pattern.
	if !LookupAnnotation(annots, 5, "missing_timeout") {
		t.Error("file-scope: line 5 should be suppressed")
	}
	if !LookupAnnotation(annots, 999, "missing_timeout") {
		t.Error("file-scope: any line should be suppressed for the pattern")
	}
	if LookupAnnotation(annots, 5, "rate_limit_inference") {
		t.Error("file-scope per-pattern should not match a different pattern")
	}
}

func TestAnnotations_AllScope(t *testing.T) {
	src := `// orion:ignore-all reason="experimental file"
package x

func F() {
    callA()
}
`
	annots, warnings := ParseFile([]byte(src))
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(annots) != 1 {
		t.Fatalf("got %d annotations, want 1", len(annots))
	}
	if annots[0].Scope != ScopeAll {
		t.Errorf("got scope %q", annots[0].Scope)
	}
	if !LookupAnnotation(annots, 5, "missing_timeout") {
		t.Error("ignore-all should suppress missing_timeout")
	}
	if !LookupAnnotation(annots, 5, "any_other_pattern") {
		t.Error("ignore-all should suppress any pattern")
	}
}

func TestAnnotations_MalformedEmitsWarningAndDoesNotSuppress(t *testing.T) {
	src := `package x

// orion:ignore
// orion:ignore missing_timeout
// orion:ignore-all
func F() {
    callA()
}
`
	annots, warnings := ParseFile([]byte(src))
	if len(warnings) < 2 {
		t.Errorf("expected at least 2 warnings, got %d: %v", len(warnings), warnings)
	}
	// "// orion:ignore" with no pattern is malformed.
	// "// orion:ignore missing_timeout" without reason= is malformed (we require reason).
	// "// orion:ignore-all" at line 5 is missing reason=; malformed.
	for _, w := range warnings {
		if !strings.Contains(strings.ToLower(w.Message), "malformed") &&
			!strings.Contains(strings.ToLower(w.Message), "reason") &&
			!strings.Contains(strings.ToLower(w.Message), "pattern") {
			t.Errorf("warning message unclear: %q", w.Message)
		}
	}
	// Even though we saw the lines, no annotation should suppress
	// anything (over-detect on malformed input is the v1 contract).
	if LookupAnnotation(annots, 7, "missing_timeout") {
		t.Error("malformed annotations must not suppress")
	}
}

func TestAnnotations_EmptyFile(t *testing.T) {
	annots, warnings := ParseFile([]byte(""))
	if len(annots) != 0 || len(warnings) != 0 {
		t.Errorf("empty file produced annotations=%d warnings=%d", len(annots), len(warnings))
	}
}

func TestAnnotations_LineFormOnlySuppressesNextNonBlankLine(t *testing.T) {
	src := `package x

func F() {
    // orion:ignore missing_timeout reason="r"

    callTheThing()
}
`
	annots, _ := ParseFile([]byte(src))
	if len(annots) != 1 {
		t.Fatalf("got %d annotations, want 1", len(annots))
	}
	// The annotation is on line 4; the next non-blank line is 6.
	if !LookupAnnotation(annots, 6, "missing_timeout") {
		t.Error("line-form should jump over blank lines to suppress next non-blank statement line")
	}
}
