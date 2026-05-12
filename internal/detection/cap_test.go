package detection

import "testing"

func TestProgressiveCap_CapLimit(t *testing.T) {
	cases := []struct {
		name             string
		maxPerRun        int
		targetDepth      int
		eligibleCount    int
		want             int
	}{
		{"target-current binds (10 headroom < 25 max)", 25, 20, 10, 10},
		{"target-current binds (20 headroom < 25 max)", 25, 20, 0, 20},
		{"max_per_run binds (25 max < 100 headroom)", 25, 100, 0, 25},
		{"backlog exceeded → no fillings", 25, 20, 25, 0},
		{"backlog equal → no fillings", 25, 20, 20, 0},
		{"no target → max_per_run only", 25, 0, 5, 25},
		{"defaults: 0/0 → DefaultMaxPerRun", 0, 0, 0, DefaultMaxPerRun},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cap := ProgressiveDisclosureCap{
				MaxPerRun:     c.maxPerRun,
				TargetDepth:   c.targetDepth,
				EligibleCount: c.eligibleCount,
			}
			if got := cap.CapLimit(); got != c.want {
				t.Errorf("CapLimit = %d, want %d", got, c.want)
			}
		})
	}
}

func mkFinding(slug, file string, line int, fp string) Finding {
	return Finding{Slug: slug, File: file, Line: line, Fingerprint: fp}
}

func TestProgressiveCap_Filter_30Findings_TargetMinusCurrent(t *testing.T) {
	// new-gap-set of 30, max_per_run=25, target_depth=20, current=10
	// → cap returns 10 per AC.
	cap := ProgressiveDisclosureCap{MaxPerRun: 25, TargetDepth: 20, EligibleCount: 10}
	findings := make([]Finding, 30)
	for i := 0; i < 30; i++ {
		findings[i] = mkFinding("slug", "f.go", i, "fp")
	}
	got := cap.Filter(findings)
	if len(got) != 10 {
		t.Errorf("Filter returned %d, want 10", len(got))
	}
}

func TestProgressiveCap_Filter_30Findings_FullHeadroom(t *testing.T) {
	// 30 findings, max=25, target=20, current=0 → cap returns 20.
	cap := ProgressiveDisclosureCap{MaxPerRun: 25, TargetDepth: 20, EligibleCount: 0}
	findings := make([]Finding, 30)
	for i := 0; i < 30; i++ {
		findings[i] = mkFinding("slug", "f.go", i, "fp")
	}
	got := cap.Filter(findings)
	if len(got) != 20 {
		t.Errorf("Filter returned %d, want 20", len(got))
	}
}

func TestProgressiveCap_Filter_30Findings_MaxPerRunBinds(t *testing.T) {
	// 30 findings, max=25, target=100, current=0 → cap returns 25.
	cap := ProgressiveDisclosureCap{MaxPerRun: 25, TargetDepth: 100, EligibleCount: 0}
	findings := make([]Finding, 30)
	for i := 0; i < 30; i++ {
		findings[i] = mkFinding("slug", "f.go", i, "fp")
	}
	got := cap.Filter(findings)
	if len(got) != 25 {
		t.Errorf("Filter returned %d, want 25", len(got))
	}
}

func TestProgressiveCap_Filter_NoTruncationNeeded(t *testing.T) {
	// 5 findings, any cap → cap returns 5.
	cap := ProgressiveDisclosureCap{MaxPerRun: 25, TargetDepth: 20, EligibleCount: 0}
	findings := []Finding{
		mkFinding("s", "a", 1, "fp1"),
		mkFinding("s", "b", 2, "fp2"),
		mkFinding("s", "c", 3, "fp3"),
		mkFinding("s", "d", 4, "fp4"),
		mkFinding("s", "e", 5, "fp5"),
	}
	got := cap.Filter(findings)
	if len(got) != 5 {
		t.Errorf("Filter returned %d, want 5", len(got))
	}
}

func TestProgressiveCap_Filter_OrdersByTrustScoreDesc(t *testing.T) {
	findings := []Finding{
		mkFinding("low", "a.go", 1, "fp1"),
		mkFinding("high", "b.go", 2, "fp2"),
		mkFinding("low", "c.go", 3, "fp3"),
		mkFinding("high", "d.go", 4, "fp4"),
	}
	cap := ProgressiveDisclosureCap{
		MaxPerRun: 4,
		TrustScoreFn: func(s string) float64 {
			if s == "high" {
				return 5.0
			}
			return 1.0
		},
	}
	got := cap.Filter(findings)
	if len(got) != 4 {
		t.Fatalf("Filter returned %d, want 4", len(got))
	}
	if got[0].Slug != "high" || got[1].Slug != "high" {
		t.Errorf("expected high-trust first; got %s,%s,%s,%s",
			got[0].Slug, got[1].Slug, got[2].Slug, got[3].Slug)
	}
}

func TestProgressiveCap_Filter_StableTieBreak(t *testing.T) {
	// With default 1.0 trust, ordering should fall through to
	// fingerprint then file then line (FIFO-ish on stable inputs).
	findings := []Finding{
		mkFinding("s", "z.go", 10, "fp-z"),
		mkFinding("s", "a.go", 1, "fp-a"),
		mkFinding("s", "m.go", 5, "fp-m"),
	}
	cap := ProgressiveDisclosureCap{MaxPerRun: 3}
	got := cap.Filter(findings)
	if len(got) != 3 {
		t.Fatalf("Filter returned %d, want 3", len(got))
	}
	if got[0].Fingerprint != "fp-a" || got[1].Fingerprint != "fp-m" || got[2].Fingerprint != "fp-z" {
		t.Errorf("unexpected order: %s, %s, %s",
			got[0].Fingerprint, got[1].Fingerprint, got[2].Fingerprint)
	}
}

func TestProgressiveCap_Filter_DoesNotMutateInput(t *testing.T) {
	input := []Finding{
		mkFinding("low", "a.go", 1, "fp1"),
		mkFinding("high", "b.go", 2, "fp2"),
	}
	cap := ProgressiveDisclosureCap{
		MaxPerRun: 1,
		TrustScoreFn: func(s string) float64 {
			if s == "high" {
				return 5.0
			}
			return 1.0
		},
	}
	_ = cap.Filter(input)
	if input[0].Slug != "low" || input[1].Slug != "high" {
		t.Errorf("Filter mutated input: %+v", input)
	}
}
