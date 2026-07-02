package casecheck

import (
	_ "embed"
	"strings"
)

//go:embed casecheck.go
var source string

// Source returns the oracle source with its package clause rewritten, so
// testsynth can ship the IDENTICAL semantics inside the generated corpus
// (package main) — one implementation, two compilation contexts.
func Source(pkg string) string {
	i := strings.Index(source, "package casecheck")
	return source[:i] + "package " + pkg + source[i+len("package casecheck"):]
}
