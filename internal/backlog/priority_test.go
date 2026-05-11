package backlog

import (
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/repos"
)

// p constructs a NormalizedIssue with explicit priority for tests.
func makeIssue(externalID string, priority *int16, createdAt time.Time) repos.NormalizedIssue {
	return repos.NormalizedIssue{
		ExternalID: externalID,
		Priority:   priority,
		CreatedAt:  createdAt,
	}
}

func ptr(v int16) *int16 { return &v }

func TestCompare_PriorityCriticalFirst(t *testing.T) {
	now := time.Now().UTC()
	critical := makeIssue("gh:x#1", ptr(0), now)
	medium := makeIssue("gh:x#2", ptr(3), now)
	if Compare(critical, medium) >= 0 {
		t.Error("critical priority should sort before medium")
	}
}

func TestCompare_NilPriorityLast(t *testing.T) {
	now := time.Now().UTC()
	withPrio := makeIssue("gh:x#1", ptr(3), now)
	noPrio := makeIssue("gh:x#2", nil, now)
	if Compare(withPrio, noPrio) >= 0 {
		t.Error("issue with priority should sort before nil-priority issue")
	}
}

func TestCompare_FIFOOnEqualPriority(t *testing.T) {
	older := makeIssue("gh:x#1", ptr(2), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	newer := makeIssue("gh:x#2", ptr(2), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if Compare(older, newer) >= 0 {
		t.Error("older issue should sort first when priority equal (FIFO)")
	}
}

func TestCompare_ExternalIDTiebreak(t *testing.T) {
	now := time.Now().UTC()
	a := makeIssue("gh:x#1", ptr(2), now)
	b := makeIssue("gh:x#2", ptr(2), now)
	if Compare(a, b) >= 0 {
		t.Error("alphabetically lower external_id should sort first")
	}
}

func TestSort_Deterministic(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	issues := []repos.NormalizedIssue{
		makeIssue("gh:repo#z", ptr(3), t1),
		makeIssue("gh:repo#a", ptr(0), t2),
		makeIssue("gh:repo#m", nil, t1),
		makeIssue("gh:repo#b", ptr(0), t2),
		makeIssue("gh:repo#k", ptr(3), t1),
	}
	expected := []string{"gh:repo#a", "gh:repo#b", "gh:repo#k", "gh:repo#z", "gh:repo#m"}

	Sort(issues)
	got := make([]string, len(issues))
	for i, iss := range issues {
		got[i] = iss.ExternalID
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Errorf("position %d: got %q, want %q (full got=%v)", i, got[i], expected[i], got)
		}
	}

	// Re-sort with the same data: result is identical (determinism).
	issues2 := []repos.NormalizedIssue{
		makeIssue("gh:repo#z", ptr(3), t1),
		makeIssue("gh:repo#a", ptr(0), t2),
		makeIssue("gh:repo#m", nil, t1),
		makeIssue("gh:repo#b", ptr(0), t2),
		makeIssue("gh:repo#k", ptr(3), t1),
	}
	Sort(issues2)
	for i := range issues {
		if issues[i].ExternalID != issues2[i].ExternalID {
			t.Errorf("non-deterministic at %d: %s vs %s", i, issues[i].ExternalID, issues2[i].ExternalID)
		}
	}
}
