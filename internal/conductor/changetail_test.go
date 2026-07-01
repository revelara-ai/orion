package conductor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/actuation"
)

// TestSecretFindingsInChangedFilesOnly: the change-flow security gate judges the
// CHANGE, not the repo — a pre-existing secret elsewhere never blocks, a secret
// the change introduces always does.
func TestSecretFindingsInChangedFilesOnly(t *testing.T) {
	dir := t.TempDir()
	dogWrite(t, filepath.Join(dir, "legacy.go"), "package p\n\nvar apiKey = \"secret: sk-legacy-1234567\"\n")
	dogWrite(t, filepath.Join(dir, "clean.go"), "package p\n\nfunc ok() {}\n")
	dogWrite(t, filepath.Join(dir, "bad.go"), "package p\n\n// password = \"hunter2-hunter2\"\nvar password = \"hunter2-hunter2\"\n")

	if f := secretFindingsInChanged(dir, []string{"clean.go"}); len(f) != 0 {
		t.Errorf("pre-existing repo findings must not block a clean change, got %v", f)
	}
	f := secretFindingsInChanged(dir, []string{"clean.go", "bad.go"})
	if len(f) == 0 {
		t.Fatal("a secret introduced by the change must be found")
	}
	for _, finding := range f {
		if strings.HasPrefix(finding, "legacy.go") {
			t.Errorf("finding outside the change set leaked into the gate: %v", f)
		}
	}
}

// TestChangeRefusedWhileRedButtonEngaged: the change flow is outward actuation —
// an engaged red button must leave the change generated but NOT committed, with
// the guard's reason. (This was the audit's hole: orion change committed even
// while the button was engaged.)
func TestChangeRefusedWhileRedButtonEngaged(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the regression gate (go test); skipped in -short")
	}
	repo := initDogfoodRepo(t)
	store := openStore(t)
	var gotKind string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e struct {
			Kind string `json:"kind"`
		}
		_ = json.NewDecoder(r.Body).Decode(&e)
		gotKind = e.Kind
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("ORION_NOTIFY_WEBHOOK", srv.URL)
	rb := actuation.RedButton{Path: filepath.Join(store.Dir(), "red_button")}
	if err := rb.Engage(); err != nil {
		t.Fatal(err)
	}

	stub := &stubGen{files: map[string]string{"note.go": "package dogfood\n\n// Note documents the module.\nfunc Note() string { return \"noted\" }\n"}}
	res, err := ChangeAndProve(context.Background(), repo, store, stub, "add a Note helper", nil, nil)
	if err != nil {
		t.Fatalf("an engaged button is a refusal, not an error: %v", err)
	}
	if res.Committed {
		t.Fatal("the change must NOT commit while the red button is engaged")
	}
	if !strings.Contains(res.Reason, "red button") {
		t.Errorf("the refusal must carry the guard reason, got: %q", res.Reason)
	}
	if res.Delivery != "escalate" {
		t.Errorf("a refused change is an escalate decision, got %q", res.Delivery)
	}
	if gotKind != "change.escalated" {
		t.Errorf("a refused change must notify out-of-band, got kind %q", gotKind)
	}
}
