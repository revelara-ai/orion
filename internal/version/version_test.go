package version

import (
	"strings"
	"testing"
)

func TestStringIncludesVersion(t *testing.T) {
	got := String()
	if got == "" {
		t.Fatal("String() returned empty; want non-empty version line")
	}
	if !strings.Contains(got, Version) {
		t.Errorf("String()=%q does not contain Version=%q", got, Version)
	}
}

func TestVersionDefaultsAreNonEmpty(t *testing.T) {
	if Version == "" {
		t.Error("Version is empty; want a default like 'dev' when not set via -ldflags")
	}
}
