package memory

import (
	"context"
	"testing"
)

// TestMemoryStoreContextStoreDivergenceDetected (or-ha0z, North-Star
// predicate): a pinned decision item that CONTRADICTS the context store's
// current decisions is reported (item, key, both values); agreement, unrelated
// keys, non-canonical content, and OTHER projects' items report nothing.
func TestMemoryStoreContextStoreDivergenceDetected(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	proj := s.ForProject("proj-a")

	staleID, err := proj.Write(ctx, Item{
		Tier: MTM, Kind: KindDecision, TrustTier: TrustProof, Heat: 1.0,
		Content: "spec pins:\ndecision direction.language = go\ndecision response_format = json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := proj.Pin(ctx, staleID); err != nil {
		t.Fatal(err)
	}
	// Prose without canonical lines: never a false positive.
	if _, err := proj.Write(ctx, Item{Tier: MTM, Kind: KindPattern, TrustTier: TrustProof, Heat: 1.0,
		Content: "we once talked about go here, informally"}); err != nil {
		t.Fatal(err)
	}
	// Another project's contradicting pin must NOT be scanned under proj-a.
	// (ForProject re-scopes the SAME handle, so scope back afterwards.)
	if _, err := s.ForProject("proj-b").Write(ctx, Item{Tier: MTM, Kind: KindDecision, TrustTier: TrustProof, Heat: 1.0,
		Content: "decision direction.language = rust"}); err != nil {
		t.Fatal(err)
	}
	proj = s.ForProject("proj-a")

	// The context store has moved on: language is python now; format unchanged.
	current := map[string]string{"direction.language": "python", "response_format": "json", "port": "8080"}
	div, err := proj.DetectDivergence(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(div) != 1 {
		t.Fatalf("exactly the contradicted key must report, got %+v", div)
	}
	d := div[0]
	if d.ItemID != staleID || d.Key != "direction.language" || d.Stored != "go" || d.Current != "python" {
		t.Fatalf("divergence must carry item+key+both values, got %+v", d)
	}

	// Agreement everywhere → nothing.
	agree := map[string]string{"direction.language": "go", "response_format": "json"}
	if div, err := proj.DetectDivergence(ctx, agree); err != nil || len(div) != 0 {
		t.Fatalf("agreement must report nothing, got %+v (err %v)", div, err)
	}
	// No current facts → nothing (vacuous, not an error).
	if div, err := proj.DetectDivergence(ctx, nil); err != nil || div != nil {
		t.Fatalf("empty current must be vacuous, got %+v (err %v)", div, err)
	}
}
