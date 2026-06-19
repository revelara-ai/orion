package dependencyprovenance

import (
	"context"
	"testing"
)

type fakeResolver struct{ info Info }

func (f fakeResolver) Resolve(context.Context, string) (Info, error) { return f.info, nil }

// TestPreRegisteredTyposquatRejected: a package that EXISTS but is anomalous
// (very new + low adoption) is rejected — existence alone is not safe.
func TestPreRegisteredTyposquatRejected(t *testing.T) {
	p := DefaultPolicy()

	// Exists-but-anomalous → rejected.
	typo := Verify(context.Background(), fakeResolver{Info{Exists: true, FirstPublishAgeDays: 2, Popularity: 0}}, "github.com/evil/reqeusts", p)
	if typo.OK {
		t.Fatalf("anomalous typosquat should be rejected: %s", typo.Reason)
	}

	// Nonexistent → rejected.
	missing := Verify(context.Background(), fakeResolver{Info{Exists: false}}, "github.com/nope/nope", p)
	if missing.OK {
		t.Fatal("nonexistent dependency should be rejected")
	}

	// Real, established, adopted → accepted.
	good := Verify(context.Background(), fakeResolver{Info{Exists: true, FirstPublishAgeDays: 1000, Popularity: 500}}, "github.com/stretchr/testify", p)
	if !good.OK {
		t.Fatalf("established dependency should pass: %s", good.Reason)
	}
}
