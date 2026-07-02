package conductor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/actuation"
	"github.com/revelara-ai/orion/internal/contextstore"
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
	// or-v9f.15: a failed change lands in the unified inbox under the reserved
	// brownfield holder, so it is actionable via `orion escalations list`.
	if res.EscalationID == "" {
		t.Fatal("a refused change must record an inbox escalation")
	}
	ctx := context.Background()
	if err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		open, e := tx.Escalations().ListOpen(ctx)
		if e != nil {
			return e
		}
		found := false
		for _, esc := range open {
			if esc.ID == res.EscalationID {
				found = true
				if !strings.Contains(esc.Detail, "review branch") {
					t.Errorf("escalation detail must carry the review branch, got: %q", esc.Detail)
				}
			}
		}
		if !found {
			t.Fatal("the change escalation must appear in the open inbox")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestChangeDeliversPRArtifact (or-v9f.15): a proven, committed change produces a
// PR-ready artifact over its review branch, reusing the greenfield machinery.
func TestChangeDeliversPRArtifact(t *testing.T) {
	requireSandboxAndLint(t)
	repo := initDogfoodRepo(t)
	store := openStore(t)
	stub := &stubGen{files: map[string]string{".golangci.yml": dogfoodGolangci, "Makefile": dogfoodMakefile}}

	res, err := ChangeAndProve(context.Background(), repo, store, stub,
		"add a golangci-lint config (v2, enable staticcheck, exclude archive/) and Makefile lint+vet targets",
		dogfoodCases(), nil)
	if err != nil {
		t.Fatalf("ChangeAndProve: %v", err)
	}
	if !res.Committed || res.Delivery != "deliver" {
		t.Fatalf("an honest, proven change must deliver; got committed=%v delivery=%q reason=%q", res.Committed, res.Delivery, res.Reason)
	}
	if res.PR.ArtifactPath == "" {
		t.Fatal("a delivered change must produce a PR-ready artifact")
	}
	body, rerr := os.ReadFile(res.PR.ArtifactPath)
	if rerr != nil {
		t.Fatalf("PR artifact not written: %v", rerr)
	}
	if !strings.Contains(string(body), "Brownfield change") || !strings.Contains(string(body), "golangci") {
		t.Errorf("PR body missing change provenance:\n%s", body)
	}
	joined := strings.Join(res.PR.Commands, "\n")
	if !strings.Contains(joined, "push -u origin") || !strings.Contains(joined, res.Branch) {
		t.Errorf("PR handoff must record the push command for the review branch, got: %v", res.PR.Commands)
	}
}
