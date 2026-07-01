package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// TestSubmitEnqueuesBehindActive: a second intent must QUEUE behind the active one,
// not silently orphan it — the in-flight work item stays the first project.
func TestSubmitEnqueuesBehindActive(t *testing.T) {
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c := NewWithStore(store)
	ctx := context.Background()

	if _, err := c.Submit(ctx, "Build service ALPHA with an HTTP endpoint"); err != nil {
		t.Fatal(err)
	}
	conf, err := c.Submit(ctx, "Build service BETA with an HTTP endpoint")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(conf.Message), "queue") {
		t.Errorf("second submit should say it queued, got: %q", conf.Message)
	}

	p, _, err := store.CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Intent, "ALPHA") {
		t.Errorf("the in-flight project must remain ALPHA (BETA queued), got intent: %q", p.Intent)
	}
}

// TestCurrentProjectSpecResolvesActiveNotLatest: creation order must not decide the
// in-flight work item; the single active project does.
func TestCurrentProjectSpecResolvesActiveNotLatest(t *testing.T) {
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c := NewWithStore(store)
	ctx := context.Background()

	if _, err := c.Submit(ctx, "Build service ALPHA with an HTTP endpoint"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Submit(ctx, "Build service BETA with an HTTP endpoint"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Submit(ctx, "Build service GAMMA with an HTTP endpoint"); err != nil {
		t.Fatal(err)
	}

	p, _, err := store.CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Intent, "ALPHA") {
		t.Errorf("active project must be ALPHA (BETA, GAMMA queued FIFO), got: %q", p.Intent)
	}
}
