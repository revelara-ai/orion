package patches

import (
	"errors"
	"testing"
)

const goodTimeoutDiff = `--- a/client.go
+++ b/client.go
@@ -10,3 +10,5 @@
 func Call(ctx context.Context) error {
+	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
+	defer cancel()
 	return doIt(ctx)
 }
`

const goodRetryDiff = `--- a/retry.go
+++ b/retry.go
@@ -1,5 +1,8 @@
 package svc

+import "github.com/cenkalti/backoff/v4"
+
 func Try() error {
+	return backoff.Retry(do, backoff.NewExponentialBackOff())
 }
`

const goodIdempotencyDiff = `--- a/handler.go
+++ b/handler.go
@@ -1,3 +1,6 @@
 func Post(w http.ResponseWriter, r *http.Request) {
+	if k := r.Header.Get("Idempotency-Key"); k != "" {
+		// dedupe
+	}
 }
`

func TestGrammarRejectsMissingHeader(t *testing.T) {
	g := GrammarFor(PatternTimeout)
	err := g.Validate("just some text\n+context.WithTimeout(...)\n")
	if !errors.Is(err, ErrInvalidDiff) {
		t.Errorf("expected ErrInvalidDiff, got %v", err)
	}
}

func TestGrammarRejectsMissingHunk(t *testing.T) {
	g := GrammarFor(PatternTimeout)
	err := g.Validate("--- a/x\n+++ b/x\n+context.WithTimeout(ctx)\n")
	if !errors.Is(err, ErrInvalidDiff) {
		t.Errorf("expected ErrInvalidDiff, got %v", err)
	}
}

func TestGrammarRejectsMissingHint(t *testing.T) {
	g := GrammarFor(PatternTimeout)
	d := "--- a/x\n+++ b/x\n@@ -1,1 +1,2 @@\n line1\n+unrelated change\n"
	err := g.Validate(d)
	if !errors.Is(err, ErrInvalidDiff) {
		t.Errorf("expected ErrInvalidDiff (missing hint), got %v", err)
	}
}

func TestGrammarRejectsOversizedDiff(t *testing.T) {
	g := GrammarFor(PatternTimeout)
	d := "--- a/x\n+++ b/x\n@@ -1,1 +1,200 @@\n"
	for i := 0; i < 100; i++ {
		d += "+context.WithTimeout(ctx, 1*time.Second)\n"
	}
	err := g.Validate(d)
	if !errors.Is(err, ErrInvalidDiff) {
		t.Errorf("expected ErrInvalidDiff (oversized), got %v", err)
	}
}

func TestGrammarAcceptsGoodDiffs(t *testing.T) {
	cases := []struct {
		name string
		p    Pattern
		d    string
	}{
		{"timeout", PatternTimeout, goodTimeoutDiff},
		{"retry", PatternRetry, goodRetryDiff},
		{"idempotency", PatternIdempotency, goodIdempotencyDiff},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := GrammarFor(c.p)
			if err := g.Validate(c.d); err != nil {
				t.Errorf("validate(%s): %v", c.name, err)
			}
		})
	}
}

func TestSupportedPatternsCoverage(t *testing.T) {
	got := SupportedPatterns()
	want := map[Pattern]bool{PatternTimeout: false, PatternRetry: false, PatternIdempotency: false}
	for _, p := range got {
		want[p] = true
	}
	for p, hit := range want {
		if !hit {
			t.Errorf("SupportedPatterns missing %q", p)
		}
	}
}
