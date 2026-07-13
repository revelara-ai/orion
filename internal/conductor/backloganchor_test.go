package conductor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// fakeBd installs a bd shim that serves a controllable issue JSON from a file,
// so tests can simulate an EXTERNAL edit between intake and validation.
func fakeBd(t *testing.T) (setIssue func(desc string)) {
	t.Helper()
	dir := t.TempDir()
	payload := filepath.Join(dir, "issue.json")
	shim := filepath.Join(dir, "bd")
	script := fmt.Sprintf("#!/bin/sh\ncat %q\n", payload)
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil { // #nosec G306 -- executable shim
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func(desc string) {
		j := fmt.Sprintf(`[{"id":"xx-1","title":"t","description":%q,"labels":[],"priority":2,"issue_type":"task","status":"open","updated_at":"now"}]`, desc)
		if err := os.WriteFile(payload, []byte(j), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func testAnchorStore(t *testing.T) (*contextstore.Store, string) {
	t.Helper()
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var projID string
	if err := store.WithTx(context.Background(), func(tx *contextstore.Tx) error {
		id, cerr := tx.Projects().Create(context.Background(), "p", "intent", "greenfield")
		projID = id
		return cerr
	}); err != nil {
		t.Fatal(err)
	}
	return store, projID
}

// The or-v9f.7 contract end to end: intake anchors the issue; an external edit
// is DRIFT even if the brain re-reads the edited issue afterwards (first read
// wins — re-anchoring onto the edit would mask exactly what this surfaces);
// reverting the edit clears the drift.
func TestBacklogAnchorSurfacesExternalEdit(t *testing.T) {
	setIssue := fakeBd(t)
	store, projID := testAnchorStore(t)
	ctx := context.Background()

	setIssue("original intake description")
	recordBacklogAnchor(ctx, store, projID, t.TempDir(), "xx-1")

	if detail, drift := backlogDriftCheck(ctx, store, projID, t.TempDir()); drift {
		t.Fatalf("unedited issue reported as drift: %s", detail)
	}

	setIssue("EXTERNALLY EDITED mid-run")
	detail, drift := backlogDriftCheck(ctx, store, projID, t.TempDir())
	if !drift {
		t.Fatal("external edit not surfaced as drift")
	}
	if !strings.Contains(detail, "xx-1") || !strings.Contains(detail, "edited since intake") {
		t.Fatalf("drift detail must name the issue and the cause: %s", detail)
	}

	// First read wins: re-reading the EDITED issue must not move the anchor.
	recordBacklogAnchor(ctx, store, projID, t.TempDir(), "xx-1")
	if _, drift := backlogDriftCheck(ctx, store, projID, t.TempDir()); !drift {
		t.Fatal("re-read after the edit silently re-anchored — intake snapshot must be immutable")
	}

	setIssue("original intake description")
	if detail, drift := backlogDriftCheck(ctx, store, projID, t.TempDir()); drift {
		t.Fatalf("reverted issue still reported as drift: %s", detail)
	}
}

// Lifecycle writes are NOT drift: the run itself claims/closes issues, so
// status/assignee/updated_at are excluded from the anchored content.
func TestBacklogAnchorIgnoresLifecycleFields(t *testing.T) {
	dir := t.TempDir()
	payload := filepath.Join(dir, "issue.json")
	shim := filepath.Join(dir, "bd")
	if err := os.WriteFile(shim, []byte(fmt.Sprintf("#!/bin/sh\ncat %q\n", payload)), 0o755); err != nil { // #nosec G306
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	write := func(status, assignee string) {
		j := fmt.Sprintf(`[{"id":"xx-1","title":"t","description":"d","labels":[],"priority":2,"issue_type":"task","status":%q,"assignee":%q,"updated_at":%q}]`, status, assignee, status+assignee)
		if err := os.WriteFile(payload, []byte(j), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store, projID := testAnchorStore(t)
	ctx := context.Background()

	write("open", "")
	recordBacklogAnchor(ctx, store, projID, dir, "xx-1")
	write("in_progress", "orion")
	if detail, drift := backlogDriftCheck(ctx, store, projID, dir); drift {
		t.Fatalf("claim/status change misread as content drift: %s", detail)
	}
}

// An anchored issue that disappears from the tracker is drift too.
func TestBacklogAnchorUnreadableIssueIsDrift(t *testing.T) {
	setIssue := fakeBd(t)
	store, projID := testAnchorStore(t)
	ctx := context.Background()

	setIssue("here today")
	recordBacklogAnchor(ctx, store, projID, t.TempDir(), "xx-1")

	// Shim now fails: issue gone.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil { // #nosec G306
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	detail, drift := backlogDriftCheck(ctx, store, projID, t.TempDir())
	if !drift || !strings.Contains(detail, "no longer readable") {
		t.Fatalf("vanished issue not surfaced: drift=%v detail=%s", drift, detail)
	}
}

// No anchors → no drift, no noise.
func TestBacklogDriftCheckEmptyIsClean(t *testing.T) {
	store, projID := testAnchorStore(t)
	if detail, drift := backlogDriftCheck(context.Background(), store, projID, t.TempDir()); drift || detail != "" {
		t.Fatalf("empty anchor set must be silent: drift=%v detail=%q", drift, detail)
	}
}
