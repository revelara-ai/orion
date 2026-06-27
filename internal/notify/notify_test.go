package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestNotifyWebhookPOST: a configured webhook receives the event as JSON (or-ykz.18).
func TestNotifyWebhookPOST(t *testing.T) {
	var got Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	want := Event{Kind: "completed", Task: "T1", Verdict: "Accept", Detail: "deliver"}
	if err := notify(context.Background(), want, srv.URL, srv.Client()); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("webhook received %+v, want %+v", got, want)
	}
}

// TestNotifyNoWebhookIsNoOp: with no webhook configured, Notify logs and returns nil.
func TestNotifyNoWebhookIsNoOp(t *testing.T) {
	if err := notify(context.Background(), Event{Kind: "completed", Task: "T2"}, "", http.DefaultClient); err != nil {
		t.Fatalf("no webhook should be a no-op, got %v", err)
	}
}

// TestNotifyWebhookErrorStatus: a non-2xx webhook response is an error (surfaced to the
// fire-and-forget caller's log, never fatal).
func TestNotifyWebhookErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := notify(context.Background(), Event{Kind: "escalated"}, srv.URL, srv.Client()); err == nil {
		t.Fatal("a 500 from the webhook should be an error")
	}
}
