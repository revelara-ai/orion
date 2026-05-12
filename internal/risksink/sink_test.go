package risksink

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/repos"
)

// stubQueue is an in-memory PendingQueue for unit tests.
type stubQueue struct {
	rows []repos.RiskSinkPending
}

func (s *stubQueue) Enqueue(_ context.Context, e repos.RiskSinkPending) (repos.RiskSinkPending, error) {
	e.ID = uuid.New()
	s.rows = append(s.rows, e)
	return e, nil
}

func TestPolarisSink_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/risks" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var got Risk
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		if got.Origin != "orion-detection" {
			t.Errorf("origin = %q", got.Origin)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sink := NewPolarisSink(srv.URL, "")
	res, err := sink.Submit(context.Background(), uuid.New(), Risk{
		Origin: "orion-detection", Slug: "missing-timeout", Title: "t",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Posted || res.Status != http.StatusCreated {
		t.Errorf("Posted=%v Status=%d", res.Posted, res.Status)
	}
}

func TestPolarisSink_5xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("polaris down"))
	}))
	defer srv.Close()

	sink := NewPolarisSink(srv.URL, "")
	res, err := sink.Submit(context.Background(), uuid.New(), Risk{Slug: "x", Title: "x"})
	if err == nil {
		t.Fatal("expected error on 5xx")
	}
	if res.Posted {
		t.Errorf("Posted should be false on 5xx; got %+v", res)
	}
}

func TestLocalFallbackSink_PostsThroughOn2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	q := &stubQueue{}
	sink := NewLocalFallbackSink(NewPolarisSink(srv.URL, ""), q, srv.URL+"/risks")

	res, err := sink.Submit(context.Background(), uuid.New(), Risk{Slug: "x", Title: "x"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Posted {
		t.Errorf("expected Posted=true; got %+v", res)
	}
	if len(q.rows) != 0 {
		t.Errorf("queue should be empty on 2xx; got %d rows", len(q.rows))
	}
}

func TestLocalFallbackSink_QueuesOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	q := &stubQueue{}
	sink := NewLocalFallbackSink(NewPolarisSink(srv.URL, ""), q, srv.URL+"/risks")

	findingID := uuid.New()
	res, err := sink.Submit(context.Background(), findingID, Risk{Slug: "x", Title: "x"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Queued {
		t.Errorf("expected Queued=true; got %+v", res)
	}
	if len(q.rows) != 1 {
		t.Fatalf("queue should have 1 row; got %d", len(q.rows))
	}
	if q.rows[0].FindingID != findingID {
		t.Errorf("queue row FindingID = %s, want %s", q.rows[0].FindingID, findingID)
	}
	if q.rows[0].LastError == nil || *q.rows[0].LastError == "" {
		t.Errorf("queue row LastError should be populated")
	}
	if q.rows[0].Attempts != 1 {
		t.Errorf("queue row Attempts = %d, want 1", q.rows[0].Attempts)
	}
}

func TestLocalFallbackSink_NoUpstreamQueuesDirectly(t *testing.T) {
	q := &stubQueue{}
	sink := NewLocalFallbackSink(nil, q, "")

	findingID := uuid.New()
	res, err := sink.Submit(context.Background(), findingID, Risk{Slug: "x", Title: "x"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Queued {
		t.Errorf("expected Queued=true; got %+v", res)
	}
	if len(q.rows) != 1 {
		t.Fatalf("queue should have 1 row; got %d", len(q.rows))
	}
	if q.rows[0].Attempts != 0 {
		t.Errorf("no upstream: Attempts should be 0; got %d", q.rows[0].Attempts)
	}
}

func TestLocalFallbackSink_QueueNilReturnsError(t *testing.T) {
	sink := NewLocalFallbackSink(nil, nil, "")
	_, err := sink.Submit(context.Background(), uuid.New(), Risk{Slug: "x", Title: "x"})
	if err == nil {
		t.Error("nil queue should error")
	}
}

func TestNoopSink(t *testing.T) {
	res, err := NoopSink{}.Submit(context.Background(), uuid.New(), Risk{})
	if err != nil {
		t.Fatalf("NoopSink.Submit: %v", err)
	}
	if res.Posted || res.Queued {
		t.Errorf("NoopSink should set neither Posted nor Queued; got %+v", res)
	}
}

func TestPolarisSink_EmptyBaseURLErrors(t *testing.T) {
	sink := NewPolarisSink("", "")
	_, err := sink.Submit(context.Background(), uuid.New(), Risk{Slug: "x", Title: "x"})
	if err == nil {
		t.Error("empty base url should error")
	}
}

// Ensure errors.Is is reachable so future tests can use sentinel
// errors without removing the import.
var _ = errors.Is
