package conductor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/pkg/llm"
)

func ratifyUCA(t *testing.T, store *contextstore.Store, ucaID, hazard, disposition string, tokens []string) {
	t.Helper()
	if err := store.WithTx(context.Background(), func(tx *contextstore.Tx) error {
		pid, err := tx.Projects().GetOrCreateReserved(context.Background(), contextstore.BrownfieldProjectName, "brownfield")
		if err != nil {
			return err
		}
		return tx.RatifiedUCAs().Upsert(context.Background(), pid, ucaID, hazard, disposition, tokens)
	}); err != nil {
		t.Fatal(err)
	}
}

// The or-06lr done-when, all three clauses against the REAL change flow.
// The fixture repo's lib.go carries Add() — the "control" the baseline pins.
func TestHazardGateBlocksVanishedControl(t *testing.T) {
	if testing.Short() {
		t.Skip("git worktree + go test + LLM loop")
	}
	repo := gitInitGreenRepo(t)
	store := openStore(t)
	ratifyUCA(t, store, "UCA-1", "unguarded arithmetic path", "controlled", []string{"func Add(a, b int) int"})

	// The change REWRITES lib.go without the control token (and keeps the
	// existing test passing so the regression gate holds — the hazard gate is
	// the only thing standing).
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "write_file", `{"path":"lib.go","content":"package t\n\nfunc Add(x, y int) int { return x + y }\n"}`),
		endTurn("renamed params"),
	}}
	res, err := ChangeAndProve(context.Background(), repo, store, prov, "rename Add's parameters", nil, nil, nil)
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if res.Committed {
		t.Fatalf("a change that deletes a controlled UCA token must NOT commit: %+v", res)
	}
	if !strings.Contains(res.Reason, "hazard gate") || !strings.Contains(res.Reason, "UCA-1") {
		t.Fatalf("the refusal must be hazard-framed and name the UCA: %s", res.Reason)
	}
	if res.Delivery != "escalate" {
		t.Fatalf("vanished control must escalate: %+v", res)
	}
}

// An unrelated change (control untouched) passes the armed gate.
func TestHazardGateUnrelatedChangePasses(t *testing.T) {
	if testing.Short() {
		t.Skip("git worktree + go test + LLM loop")
	}
	repo := gitInitGreenRepo(t)
	store := openStore(t)
	ratifyUCA(t, store, "UCA-1", "unguarded arithmetic path", "controlled", []string{"func Add(a, b int) int"})

	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "write_file", `{"path":"extra.go","content":"package t\n\nfunc Mul(a, b int) int { return a * b }\n"}`),
		endTurn("added Mul"),
	}}
	res, err := ChangeAndProve(context.Background(), repo, store, prov, "add a Mul helper", nil, nil, nil)
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if !res.Committed {
		t.Fatalf("an unrelated change must pass the hazard gate: %+v", res)
	}
}

// No ratified baseline → a VISIBLE advisory skip, and the change proceeds.
func TestHazardGateAdvisorySkipWithoutBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("git worktree + go test + LLM loop")
	}
	repo := gitInitGreenRepo(t)
	store := openStore(t) // no UCAs ratified
	var lines []string
	sink := func(e PhaseEvent) { lines = append(lines, e.Phase+": "+e.Detail) }
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "write_file", `{"path":"extra.go","content":"package t\n\nfunc Mul(a, b int) int { return a * b }\n"}`),
		endTurn("added Mul"),
	}}
	res, err := ChangeAndProve(context.Background(), repo, store, prov, "add a Mul helper", nil, nil, sink)
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if !res.Committed {
		t.Fatalf("no baseline must not block: %+v", res)
	}
	skipSeen := false
	for _, l := range lines {
		if strings.Contains(l, "hazard gate") && strings.Contains(l, "advisory skip") {
			skipSeen = true
		}
	}
	if !skipSeen {
		t.Fatalf("the no-baseline state must be VISIBLE, got phases:\n%s", strings.Join(lines, "\n"))
	}
}

// Unit level: a stale token (absent even BEFORE the change) is reported but
// never blocks — a stale baseline isn't this change's fault; and only
// CONTROLLED dispositions load into the gate.
func TestHazardGateStaleAndDispositionRules(t *testing.T) {
	before, after := t.TempDir(), t.TempDir()
	mustWriteFile(t, before, "a.go", "package a\nfunc GuardA() {}\n")
	mustWriteFile(t, after, "a.go", "package a\nfunc GuardA() {}\n")

	ucas := []contextstore.RatifiedUCA{
		{UCAID: "UCA-live", Hazard: "h", Disposition: "controlled", CodeTokens: []string{"GuardA"}},
		{UCAID: "UCA-stale", Hazard: "h", Disposition: "controlled", CodeTokens: []string{"GuardNeverExisted"}},
	}
	violations, stale := hazardGate(ucas, before, after)
	if len(violations) != 0 {
		t.Fatalf("nothing vanished — no violations expected: %+v", violations)
	}
	if len(stale) != 1 || !strings.Contains(stale[0], "UCA-stale") {
		t.Fatalf("the stale token must be reported: %v", stale)
	}

	// Disposition filter: accepted-gap never loads into the gate.
	store := openStore(t)
	ratifyUCA(t, store, "UCA-c", "h", "controlled", []string{"x"})
	ratifyUCA(t, store, "UCA-gap", "h", "accepted-gap", []string{"y"})
	loaded := loadControlledUCAs(context.Background(), store)
	if len(loaded) != 1 || loaded[0].UCAID != "UCA-c" {
		t.Fatalf("only CONTROLLED UCAs gate: %+v", loaded)
	}
}

func mustWriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
