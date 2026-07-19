package memory

import (
	"context"
	"strings"
	"testing"
)

// TestDeveloperScopedLTMRedactsProjectLiterals (or-2dxc / or-gb1.6, North-Star
// predicate): widening an LTM item to developer scope REDACTS the origin
// project's literals — the generalized item is visible from another project
// but carries none of the origin's module paths/routes; a non-generalized twin
// stays invisible cross-project; redaction is case-insensitive.
func TestDeveloperScopedLTMRedactsProjectLiterals(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	content := "Proven pattern: mount /time on Orion-Generated/timeservice and validate the handler inputs"
	a := s.ForProject("proj-a")
	genID, err := a.Write(ctx, Item{Tier: LTM, Kind: KindPattern, TrustTier: TrustProof, Heat: 1.0, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	twinID, err := a.Write(ctx, Item{Tier: LTM, Kind: KindPattern, TrustTier: TrustProof, Heat: 1.0, Content: content + " (scoped twin)"})
	if err != nil {
		t.Fatal(err)
	}

	// Generalize with the origin project's literals (case differs on purpose).
	if err := a.GeneralizeItem(ctx, genID, []string{"orion-generated/timeservice", "/time"}); err != nil {
		t.Fatal(err)
	}

	// From ANOTHER project: the generalized item is visible and fully redacted.
	b := s.ForProject("proj-b")
	items, err := b.Retrieve(ctx, "proven pattern validate handler", LTM)
	if err != nil {
		t.Fatal(err)
	}
	var got *Item
	for i := range items {
		if items[i].ID == genID {
			got = &items[i]
		}
		if items[i].ID == twinID {
			t.Fatalf("a non-generalized item must NEVER be visible cross-project: %+v", items[i])
		}
	}
	if got == nil {
		t.Fatalf("the generalized item must be visible from another project, got %d items", len(items))
	}
	low := strings.ToLower(got.Content)
	if strings.Contains(low, "timeservice") || strings.Contains(low, "/time") {
		t.Fatalf("project literals must be redacted at the scope boundary:\n%s", got.Content)
	}
	if !strings.Contains(got.Content, "[redacted]") {
		t.Fatalf("redaction placeholder must mark the scrubbed spans:\n%s", got.Content)
	}

	// Unknown id refuses.
	if err := s.GeneralizeItem(ctx, "nope", nil); err == nil {
		t.Fatal("generalizing an unknown item must error")
	}
}
