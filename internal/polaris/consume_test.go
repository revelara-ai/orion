package polaris

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

func openStore(t *testing.T) *contextstore.Store {
	t.Helper()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedProject(t *testing.T, s *contextstore.Store) string {
	t.Helper()
	ctx := context.Background()
	var pid string
	_ = s.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		pid, e = tx.Projects().Create(ctx, "demo", "build a time service", "http-service")
		return e
	})
	return pid
}

// TestLoopProceedsWhenPolarisUnreachable: with Polaris unreachable and no cache,
// Load returns no error, flags reduced context, and yields empty (not nil) data —
// the loop proceeds.
func TestLoopProceedsWhenPolarisUnreachable(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	pid := seedProject(t, s)

	// A closed server's URL → connection refused immediately (unreachable).
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	consumer := NewConsumer(NewClient(closedURL), s, "tok")
	rc, err := consumer.Load(ctx, pid, "time service")
	if err != nil {
		t.Fatalf("Load must not error when Polaris is unreachable: %v", err)
	}
	if !rc.Reduced {
		t.Fatal("expected Reduced=true when Polaris is unreachable")
	}
	if rc.Sources["controls"] != SourceEmpty {
		t.Fatalf("controls source = %q, want empty", rc.Sources["controls"])
	}
	if string(rc.Controls) != "[]" {
		t.Fatalf("controls = %q, want []", rc.Controls)
	}
}

// TestCacheHitWorksOffline: a live fetch caches; a later unreachable fetch serves
// the cached payload and flags reduced context.
func TestCacheHitWorksOffline(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	pid := seedProject(t, s)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/controls":
			_, _ = w.Write([]byte(`[{"code":"C1","title":"timeouts"}]`))
		case strings.HasPrefix(r.URL.Path, "/api/knowledge/search"):
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v1/risks":
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(404)
		}
	}))

	// Live load caches all three kinds.
	live := NewConsumer(NewClient(srv.URL), s, "tok")
	rc, err := live.Load(ctx, pid, "time service")
	if err != nil {
		t.Fatalf("live load: %v", err)
	}
	if rc.Reduced || rc.Sources["controls"] != SourceLive {
		t.Fatalf("expected live, non-reduced; got reduced=%v sources=%v", rc.Reduced, rc.Sources)
	}
	offlineURL := srv.URL
	srv.Close() // now unreachable (connection refused)

	// Offline load falls back to cache.
	offline := NewConsumer(NewClient(offlineURL), s, "tok")
	rc2, err := offline.Load(ctx, pid, "time service")
	if err != nil {
		t.Fatalf("offline load: %v", err)
	}
	if !rc2.Reduced {
		t.Fatal("offline load should flag reduced context")
	}
	if rc2.Sources["controls"] != SourceCache {
		t.Fatalf("controls source = %q, want cache", rc2.Sources["controls"])
	}
	if !strings.Contains(string(rc2.Controls), "timeouts") {
		t.Fatalf("cached controls not served offline: %s", rc2.Controls)
	}
}
