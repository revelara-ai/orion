package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// TestDialogueDominatesGatesOnReducibleMessages: compaction only shrinks the
// MESSAGE history — never the fixed system+tool floor — so its gate must measure
// the reducible message portion. A tiny history must NOT report domination just
// because the tool/system floor is large (that would compact every turn); a large
// dialogue must.
func TestDialogueDominatesGatesOnReducibleMessages(t *testing.T) {
	prov := &recordingWindowLLM{window: 8192} // CompactAt = 5734
	tiny := []llm.Message{llm.TextMessage(llm.RoleUser, "hi")}
	if dialogueDominates(tiny, prov) {
		t.Fatal("dialogueDominates fired on a tiny history — spurious compaction from the irreducible floor")
	}
	var big []llm.Message
	for i := 0; i < 20; i++ {
		big = append(big, llm.TextMessage(llm.RoleUser, strings.Repeat("word ", 500))) // ~12.5k tok of dialogue > CompactAt
	}
	if !dialogueDominates(big, prov) {
		t.Fatal("dialogueDominates did not fire on a large dialogue")
	}
}

// TestSplitByTokensPreservesUTF8: hard-splitting a long line must not cut a
// multi-byte rune mid-character (which would corrupt the summarizer input), and
// must be lossless.
func TestSplitByTokensPreservesUTF8(t *testing.T) {
	line := strings.Repeat("你好世界", 3000) // multi-byte runes, one long line
	chunks := splitByTokens(line, 1000)  // maxChars 4000 → forces mid-line hard-splits
	for i, c := range chunks {
		if !utf8.ValidString(c) {
			t.Fatalf("chunk %d is not valid UTF-8 — a rune was split mid-character", i)
		}
	}
	if strings.Join(chunks, "") != line {
		t.Fatal("splitByTokens lost or reordered content (not lossless)")
	}
}

// recordingWindowLLM reports a context window and records the largest request it
// was ever asked to process — so a test can prove compaction never sends a
// summarizer call that exceeds the window (the /compact booby-trap).
type recordingWindowLLM struct {
	window  int
	summary string
	maxSeen int
	calls   int
}

func (p *recordingWindowLLM) Name() string                                    { return "rec" }
func (p *recordingWindowLLM) ContextWindow() int                              { return p.window }
func (p *recordingWindowLLM) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p *recordingWindowLLM) Ping(context.Context) error                      { return nil }
func (p *recordingWindowLLM) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls++
	if n := llm.EstimateTokens(req); n > p.maxSeen {
		p.maxSeen = n
	}
	return endTurn(p.summary), nil
}
func (p *recordingWindowLLM) ChatStream(ctx context.Context, req llm.ChatRequest, onText func(string)) (*llm.ChatResponse, error) {
	return p.Chat(ctx, req)
}

// overflowThenCompactLLM rejects any over-window streamed request with
// ErrContextOverflow (the text-dominated case mechanical clearing can't fix), but
// its Chat (the summarizer path) always returns a tiny brief — so the agent can
// compact and retry the turn under the window.
type overflowThenCompactLLM struct {
	window      int
	streamCalls int
}

func (p *overflowThenCompactLLM) Name() string                                    { return "ofc" }
func (p *overflowThenCompactLLM) ContextWindow() int                              { return p.window }
func (p *overflowThenCompactLLM) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p *overflowThenCompactLLM) Ping(context.Context) error                      { return nil }
func (p *overflowThenCompactLLM) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return endTurn("BRIEF: the earlier conversation, summarized."), nil
}
func (p *overflowThenCompactLLM) ChatStream(_ context.Context, req llm.ChatRequest, onText func(string)) (*llm.ChatResponse, error) {
	p.streamCalls++
	if llm.EstimateTokens(req) > p.window {
		return nil, fmt.Errorf("boom: %w (status 400)", llm.ErrContextOverflow)
	}
	if onText != nil {
		onText("recovered after compaction")
	}
	return endTurn("recovered after compaction"), nil
}

// nonShrinkingLLM returns a fixed chunk-sized blob for EVERY summarize call, so
// chunk-summaries never get smaller — a pathological model that would spin an
// unbounded fold. Used to prove compaction always terminates.
type nonShrinkingLLM struct {
	blob  string
	calls int
}

func (p *nonShrinkingLLM) Name() string                                    { return "ns" }
func (p *nonShrinkingLLM) ContextWindow() int                              { return 8192 }
func (p *nonShrinkingLLM) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p *nonShrinkingLLM) Ping(context.Context) error                      { return nil }
func (p *nonShrinkingLLM) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls++
	return endTurn(p.blob), nil
}
func (p *nonShrinkingLLM) ChatStream(ctx context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	return p.Chat(ctx, req)
}

// TestCompactTerminatesWhenSummariesDontShrink: even if the model's "summaries"
// never get smaller, the fold is depth-bounded so compaction terminates (rather
// than recursing forever) with a bounded number of model calls.
func TestCompactTerminatesWhenSummariesDontShrink(t *testing.T) {
	prov := &nonShrinkingLLM{blob: strings.Repeat("x ", 8000)} // ~4000 tok, ~chunk-sized, never shrinks
	a := NewOrionAgent(prov, orchestrator.New(), RoleTemplate{})
	var msgs []llm.Message
	for i := 0; i < 30; i++ {
		msgs = append(msgs, llm.TextMessage(llm.RoleUser, strings.Repeat("gamma ", 800)))
	}
	a.sessions["s"] = msgs

	done := make(chan struct{})
	go func() { _, _ = a.Control(context.Background(), "s", "compact", ""); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("compaction did not terminate — the fold recursion is unbounded")
	}
	if prov.calls > 500 {
		t.Fatalf("compaction made %d model calls — the fold is not bounded", prov.calls)
	}
}

// TestPromptPersistsSessionToDisk: every turn writes a human-readable transcript
// of the session to disk, so a failing session is recoverable (the in-memory
// sessions map is otherwise lost on exit).
func TestPromptPersistsSessionToDisk(t *testing.T) {
	prov := &fakeLLM{resp: []*llm.ChatResponse{endTurn("what port should it listen on?")}}
	st := openStore(t)
	a := NewOrionAgent(prov, orchestrator.NewWithStore(st), RoleTemplate{})
	if _, err := a.Prompt(context.Background(), "sess/1", "build a time service",
		func(acp.Update) {},
		func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil }); err != nil {
		t.Fatal(err)
	}
	var found, foundName string
	dir := filepath.Join(st.Dir(), "sessions")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			found, foundName = string(b), e.Name()
		}
	}
	if !strings.Contains(found, "build a time service") {
		t.Fatalf("session not persisted under sessions/ for later inspection; got %q", found)
	}
	// filename is a timestamp stem (starts with a 4-digit year), so files sort by time
	if len(foundName) < 4 || foundName[:2] != "20" {
		t.Fatalf("transcript filename %q is not timestamp-led (should sort chronologically)", foundName)
	}
}

// TestCompactMarksPartialWhenFoldCapped: when the fold hits its depth bound (a
// non-shrinking model), the resulting brief must be MARKED partial so the dropped
// content is observable — not silently lost.
func TestCompactMarksPartialWhenFoldCapped(t *testing.T) {
	prov := &nonShrinkingLLM{blob: strings.Repeat("x ", 8000)} // never shrinks → hits the depth cap
	a := NewOrionAgent(prov, orchestrator.New(), RoleTemplate{})
	var msgs []llm.Message
	for i := 0; i < 30; i++ {
		msgs = append(msgs, llm.TextMessage(llm.RoleUser, strings.Repeat("gamma ", 800)))
	}
	a.sessions["s"] = msgs
	if _, err := a.Control(context.Background(), "s", "compact", ""); err != nil {
		t.Fatal(err)
	}
	got := a.sessions["s"][0].Content[0].Text
	if !strings.Contains(got, "compaction incomplete") {
		t.Fatalf("depth-capped compaction did not mark the brief partial: %.120q", got)
	}
}

// TestCompactTranscriptFilesAreUnique: distinct conversations must produce
// distinct transcript files. shortHash (an 8-char prefix truncation) collides on
// the constant "[user] " prefix, silently overwriting the durable record.
func TestCompactTranscriptFilesAreUnique(t *testing.T) {
	prov := &recordingWindowLLM{window: 128000, summary: "BRIEF."}
	st := openStore(t)
	a := NewOrionAgent(prov, orchestrator.NewWithStore(st), RoleTemplate{})
	a.sessions["s1"] = []llm.Message{llm.TextMessage(llm.RoleUser, "add a Severity method to Verdict")}
	a.sessions["s2"] = []llm.Message{llm.TextMessage(llm.RoleUser, "add a Timeout field to Config")}
	if _, err := a.Control(context.Background(), "s1", "compact", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Control(context.Background(), "s2", "compact", ""); err != nil {
		t.Fatal(err)
	}
	var transcripts []string
	entries, _ := os.ReadDir(st.Dir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "transcript-") {
			transcripts = append(transcripts, e.Name())
		}
	}
	if len(transcripts) != 2 {
		t.Fatalf("distinct conversations must yield 2 transcript files, got %d: %v", len(transcripts), transcripts)
	}
}

// overflowSummarizerFailsLLM overflows every streamed request AND fails the
// summarizer (Chat) — modelling a provider that's overloaded such that reactive
// compaction can't complete.
type overflowSummarizerFailsLLM struct{ streamCalls int }

func (p *overflowSummarizerFailsLLM) Name() string { return "osf" }
func (p *overflowSummarizerFailsLLM) Models(context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}
func (p *overflowSummarizerFailsLLM) Ping(context.Context) error { return nil }
func (p *overflowSummarizerFailsLLM) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, fmt.Errorf("summarizer down")
}
func (p *overflowSummarizerFailsLLM) ChatStream(context.Context, llm.ChatRequest, func(string)) (*llm.ChatResponse, error) {
	p.streamCalls++
	return nil, fmt.Errorf("boom: %w (status 400)", llm.ErrContextOverflow)
}

// TestReactiveCompactionFailureKeepsCleanSession: if reactive compaction can't
// complete, the agent must NOT persist the over-window conversation (which would
// brick every future turn) and must NOT fire a doomed retry.
func TestReactiveCompactionFailureKeepsCleanSession(t *testing.T) {
	prov := &overflowSummarizerFailsLLM{}
	a := NewOrionAgent(prov, orchestrator.NewWithStore(openStore(t)), RoleTemplate{})
	pre := []llm.Message{llm.TextMessage(llm.RoleUser, "hi"), llm.TextMessage(llm.RoleAssistant, "hello")}
	a.sessions["s"] = append([]llm.Message(nil), pre...)

	_, err := a.Prompt(context.Background(), "s", "go",
		func(acp.Update) {},
		func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := a.sessions["s"]; len(got) != len(pre) {
		t.Fatalf("compaction failure left a dirty/over-window session: %d msgs, want %d clean", len(got), len(pre))
	}
	if prov.streamCalls != 1 {
		t.Fatalf("expected no doomed retry after compaction failure, streamCalls=%d", prov.streamCalls)
	}
}

// TestReactiveGiantMessageSurfacesCleanly: a single message too large to fit even
// after compaction is reported clearly (not re-sent into a re-overflow), and the
// session is left compacted/clean rather than over-window.
func TestReactiveGiantMessageSurfacesCleanly(t *testing.T) {
	// 32k: the conductor's system+tools floor (~8.5k) FITS — the or-hhq floor
	// diagnostic stays quiet and the giant MESSAGE is what overflows.
	prov := &overflowThenCompactLLM{window: 32768}
	a := NewOrionAgent(prov, orchestrator.NewWithStore(openStore(t)), RoleTemplate{})
	a.sessions["s"] = []llm.Message{llm.TextMessage(llm.RoleUser, "prior context")}
	huge := strings.Repeat("word ", 60000) // ~75k tok, alone exceeds the 32k window

	var updates []acp.Update
	_, err := a.Prompt(context.Background(), "s", huge,
		func(u acp.Update) { updates = append(updates, u) },
		func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	var sawTooLarge bool
	for _, u := range updates {
		if strings.Contains(u.Text, "too large") {
			sawTooLarge = true
		}
	}
	if !sawTooLarge {
		t.Fatalf("giant message should surface a clear 'too large' error; updates=%v", updates)
	}
	if n := llm.EstimateTokens(llm.ChatRequest{Messages: a.sessions["s"]}); n > prov.window {
		t.Fatalf("session left over-window after a giant message: %d tokens", n)
	}
}

// TestRenderTranscriptFlattens: the transcript renderer flattens text, tool calls,
// and tool results to a plain text record (so summarization sidesteps tool_use /
// tool_result pairing rules).
func TestRenderTranscriptFlattens(t *testing.T) {
	msgs := []llm.Message{
		llm.TextMessage(llm.RoleUser, "hello there"),
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "1", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)}}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolResult: &llm.ToolResult{ToolUseID: "1", Content: "package main"}}}},
	}
	out := renderTranscript(msgs)
	for _, want := range []string{"hello there", "read_file", "a.go", "package main"} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q:\n%s", want, out)
		}
	}
}

// TestCompactNeverExceedsWindow: compacting a history far larger than the window
// must NOT send a single summarizer request that exceeds the window — it chunks
// and folds. (The old /compact sent the whole history in one call and 400'd.)
func TestCompactNeverExceedsWindow(t *testing.T) {
	prov := &recordingWindowLLM{window: 8192, summary: "BRIEF."}
	a := NewOrionAgent(prov, orchestrator.New(), RoleTemplate{})
	var msgs []llm.Message
	for i := 0; i < 20; i++ {
		msgs = append(msgs, llm.TextMessage(llm.RoleUser, strings.Repeat("alpha ", 800))) // ~1200 tok each → ~24k total
	}
	a.sessions["s"] = msgs

	res, err := a.Control(context.Background(), "s", "compact", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "Compacted") {
		t.Fatalf("compact result: %q", res)
	}
	if prov.maxSeen > prov.window {
		t.Fatalf("a summarizer call used %d tokens > window %d — compaction is NOT self-safe", prov.maxSeen, prov.window)
	}
	if got := a.sessions["s"]; len(got) != 1 {
		t.Fatalf("compaction should collapse to a single summary message, got %d", len(got))
	}
}

// TestCompactWritesTranscriptToDisk: the full pre-compaction transcript is written
// to disk as a canonical record before the lossy summary replaces it.
func TestCompactWritesTranscriptToDisk(t *testing.T) {
	prov := &recordingWindowLLM{window: 128000, summary: "BRIEF."}
	st := openStore(t)
	a := NewOrionAgent(prov, orchestrator.NewWithStore(st), RoleTemplate{})
	a.sessions["s"] = []llm.Message{
		llm.TextMessage(llm.RoleUser, "build a time service on port 8080"),
		llm.TextMessage(llm.RoleAssistant, "what timezone?"),
		llm.TextMessage(llm.RoleUser, "UTC"),
	}
	if _, err := a.Control(context.Background(), "s", "compact", ""); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(st.Dir())
	var found string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "transcript-") {
			b, _ := os.ReadFile(filepath.Join(st.Dir(), e.Name()))
			found = string(b)
		}
	}
	if !strings.Contains(found, "port 8080") || !strings.Contains(found, "UTC") {
		t.Fatalf("full transcript not persisted to disk; got %q", found)
	}
}

// firstOverflowLLM rejects the FIRST streamed request (the exact-count overflow
// the estimate can miss) then succeeds; its Chat returns a tiny brief. Used to
// exercise the REACTIVE compaction path in isolation from the proactive one.
type firstOverflowLLM struct{ streamCalls int }

func (p *firstOverflowLLM) Name() string                                    { return "fo" }
func (p *firstOverflowLLM) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p *firstOverflowLLM) Ping(context.Context) error                      { return nil }
func (p *firstOverflowLLM) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return endTurn("BRIEF: summary."), nil
}
func (p *firstOverflowLLM) ChatStream(_ context.Context, _ llm.ChatRequest, onText func(string)) (*llm.ChatResponse, error) {
	p.streamCalls++
	if p.streamCalls == 1 {
		return nil, fmt.Errorf("boom: %w (status 400)", llm.ErrContextOverflow)
	}
	if onText != nil {
		onText("recovered")
	}
	return endTurn("recovered"), nil
}

// TestPromptProactivelyCompactsWhenDialogueDominates: a text-dominated history over
// the compaction threshold is summarized BEFORE the turn, so the send fits on the
// first try (no overflow round-trip) and the session shrinks.
func TestPromptProactivelyCompactsWhenDialogueDominates(t *testing.T) {
	// Window well above the fixed tool+system floor (so the test doesn't depend on
	// that floor's exact size), with a history that exceeds CompactAt (~22.9k).
	prov := &overflowThenCompactLLM{window: 32768}
	a := NewOrionAgent(prov, orchestrator.NewWithStore(openStore(t)), RoleTemplate{})
	var msgs []llm.Message
	for i := 0; i < 30; i++ {
		msgs = append(msgs, llm.TextMessage(llm.RoleUser, strings.Repeat("beta ", 1000))) // ~37.5k tok of dialogue
	}
	a.sessions["s"] = msgs

	var updates []acp.Update
	_, err := a.Prompt(context.Background(), "s", "continue please",
		func(u acp.Update) { updates = append(updates, u) },
		func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range updates {
		if strings.Contains(u.Text, "I hit a problem") {
			t.Fatalf("session bricked: %q", u.Text)
		}
	}
	if n := llm.EstimateTokens(llm.ChatRequest{Messages: a.sessions["s"]}); n > prov.window {
		t.Fatalf("session still over window after proactive compaction: %d tokens", n)
	}
	if prov.streamCalls != 1 {
		t.Fatalf("proactive compaction should have avoided the overflow round-trip; streamCalls=%d", prov.streamCalls)
	}
}

// TestPromptReactivelyCompactsOnOverflow: when a turn overflows that the estimate
// didn't predict (so proactive compaction didn't fire) and mechanical clearing
// can't fix it, the agent compacts and retries the turn — recovering instead of
// surfacing the terminal "I hit a problem".
func TestPromptReactivelyCompactsOnOverflow(t *testing.T) {
	prov := &firstOverflowLLM{}
	a := NewOrionAgent(prov, orchestrator.NewWithStore(openStore(t)), RoleTemplate{})
	// A SMALL history (under CompactAt) so proactive compaction does NOT fire.
	a.sessions["s"] = []llm.Message{
		llm.TextMessage(llm.RoleUser, "build a time service"),
		llm.TextMessage(llm.RoleAssistant, "on what port?"),
		llm.TextMessage(llm.RoleUser, "8080"),
	}

	var updates []acp.Update
	_, err := a.Prompt(context.Background(), "s", "continue please",
		func(u acp.Update) { updates = append(updates, u) },
		func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range updates {
		if strings.Contains(u.Text, "I hit a problem") {
			t.Fatalf("session bricked instead of compacting-and-retrying: %q", u.Text)
		}
	}
	if prov.streamCalls < 2 {
		t.Fatalf("expected a retried turn after reactive compaction, streamCalls=%d", prov.streamCalls)
	}
}

// countingProvider spies exact-count calls for the cost-aware band test.
type countingProvider struct {
	ruleProvider
	window int
	count  int
	calls  int
}

func (p *countingProvider) ContextWindow() int { return p.window }
func (p *countingProvider) CountTokens(context.Context, llm.ChatRequest) (int, error) {
	p.calls++
	return p.count, nil
}

// TestFitsWindowCostAwareExactCount (or-hhq): the estimate decides when it is
// clearly on one side of the guard (no network count); only the ambiguous
// band pays for the exact count, and the exact count then decides.
func TestFitsWindowCostAwareExactCount(t *testing.T) {
	// Window 100k → GuardAt = 85k (0.85 fraction). Small convo → clearly fits,
	// no exact call.
	p := &countingProvider{window: 100_000, count: 1}
	if !fitsWindow("sys", []llm.Message{llm.TextMessage(llm.RoleUser, "hi")}, nil, p) {
		t.Fatal("a tiny convo clearly fits")
	}
	if p.calls != 0 {
		t.Fatalf("a clear verdict must not pay for an exact count, calls=%d", p.calls)
	}

	// Ambiguous band: estimate ≈ GuardAt → the exact count decides, both ways.
	amb := strings.Repeat("x", 4*85_000) // estimate ≈ 85k = GuardAt
	p.count = 90_000                     // exact says: does NOT fit
	if fitsWindow("", []llm.Message{llm.TextMessage(llm.RoleUser, amb)}, nil, p) {
		t.Fatal("the exact count says over-guard — must not fit")
	}
	if p.calls != 1 {
		t.Fatalf("the ambiguous band must consult the exact count once, calls=%d", p.calls)
	}
	p.count = 50_000 // exact says: fits fine
	if !fitsWindow("", []llm.Message{llm.TextMessage(llm.RoleUser, amb)}, nil, p) {
		t.Fatal("the exact count says under-guard — must fit")
	}

	// Clearly over: no exact call, immediate false.
	callsBefore := p.calls
	huge := strings.Repeat("x", 4*300_000)
	if fitsWindow("", []llm.Message{llm.TextMessage(llm.RoleUser, huge)}, nil, p) {
		t.Fatal("triple the window can never fit")
	}
	if p.calls != callsBefore {
		t.Fatal("a clearly-over estimate must not pay for an exact count")
	}
}
