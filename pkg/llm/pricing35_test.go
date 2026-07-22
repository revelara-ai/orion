package llm

import "testing"

// or-un7z: the Gemini 3.5 generation was missing from the table — spend
// booked $0-unpriced and the developer read the dollar gauge as broken.
// Prices verified against the official rate card 2026-07-22 (launch
// 2026-05-19): $1.50/M in, $9.00/M out, $0.15/M cache read. 3.5 Pro is NOT
// added: it has not shipped (official page says "coming soon") — the table
// carries only verified entries, per the do-not-guess rule.
func TestGemini35FlashPriced(t *testing.T) {
	p, ok := PriceFor("gemini", "gemini-3.5-flash")
	if !ok {
		t.Fatal("gemini-3.5-flash must be priced")
	}
	if p.InPerM != 1.50 || p.OutPerM != 9.00 || p.CacheReadPerM != 0.15 {
		t.Fatalf("gemini-3.5-flash pricing = %+v, want 1.50/9.00/0.15", p)
	}
	if _, ok := PriceFor("gemini", "gemini-3.5-pro"); ok {
		t.Fatal("gemini-3.5-pro has not shipped — an entry would be a guess")
	}
}
