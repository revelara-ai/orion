package conductor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/revelara-ai/orion/internal/contextwindow"
	"github.com/revelara-ai/orion/pkg/llm"
)

// Compaction is the chat-level context lever (distinct from the harness's
// universal mechanical clearing): when a conversation's bulk is TEXT/DIALOGUE that
// clearing tool results can't reduce, an LLM summarizes it into a faithful brief.
// It is self-safe — no summarizer call ever exceeds the window, even when the
// history itself is larger than the window (the /compact booby-trap) — and it
// preserves a full transcript on disk before the lossy summary replaces it.

// renderTranscript flattens a conversation to a plain-text record. Summarizing the
// TEXT (not the structured messages) sidesteps tool_use/tool_result pairing rules,
// so the transcript can be chunked at any line boundary.
func renderTranscript(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Content {
			switch blk.Type {
			case llm.BlockText:
				fmt.Fprintf(&b, "[%s] %s\n", m.Role, blk.Text)
			case llm.BlockToolUse:
				if blk.ToolUse != nil {
					fmt.Fprintf(&b, "[%s→tool] %s(%s)\n", m.Role, blk.ToolUse.Name, string(blk.ToolUse.Input))
				}
			case llm.BlockToolResult:
				if blk.ToolResult != nil {
					fmt.Fprintf(&b, "[tool_result] %s\n", blk.ToolResult.Content)
				}
			}
		}
	}
	return b.String()
}

// compactBudget is the largest input (in estimated tokens) a single summarizer
// call may use — a safe fraction of the provider's window that leaves room for the
// system prompt, the instruction, and the produced summary.
func compactBudget(prov llm.Provider) int {
	return contextwindow.For(contextwindow.WindowOf(prov, contextwindow.DefaultWindow)).CompactAt
}

// splitByTokens splits text into pieces each within tokBudget (≈ tokBudget*4
// chars), preferring line boundaries and hard-splitting any single oversize line.
func splitByTokens(text string, tokBudget int) []string {
	maxChars := tokBudget * 4
	if maxChars <= 0 || len(text) <= maxChars {
		return []string{text}
	}
	var chunks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			chunks = append(chunks, cur.String())
			cur.Reset()
		}
	}
	for _, ln := range strings.Split(text, "\n") {
		for len(ln) > maxChars {
			flush()
			cut := runeSafeCut(ln, maxChars) // never split a multi-byte rune mid-character
			chunks = append(chunks, ln[:cut])
			ln = ln[cut:]
		}
		if cur.Len() > 0 && cur.Len()+len(ln)+1 > maxChars {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteByte('\n')
		}
		cur.WriteString(ln)
	}
	flush()
	return chunks
}

// runeSafeCut returns a cut index <= maxChars that does not fall in the middle of
// a multi-byte UTF-8 rune (so a hard-split never corrupts a character). Falls back
// to maxChars if the whole prefix is one giant rune (impossible for valid UTF-8).
func runeSafeCut(s string, maxChars int) int {
	cut := maxChars
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if cut == 0 {
		cut = maxChars
	}
	return cut
}

const summarizeSystem = "You compress a long conversation into a faithful brief without losing decisions, code facts, file paths, or open threads."

// summarizeOne summarizes a single transcript chunk (assumed within budget).
func summarizeOne(ctx context.Context, prov llm.Provider, chunk string) (string, error) {
	resp, err := prov.Chat(ctx, llm.ChatRequest{
		System: summarizeSystem,
		Messages: []llm.Message{llm.TextMessage(llm.RoleUser,
			"Summarize this transcript segment into a concise brief that preserves EVERY decision, code fact, file path, ratified spec detail, and open question. Output only the summary.\n\n<transcript>\n"+chunk+"\n</transcript>")},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Text()), nil
}

// maxFoldDepth bounds the fold recursion so a misbehaving model whose "summaries"
// never shrink can't spin forever — after this many folds we summarize the most
// recent chunk and return a MARKED-partial brief (always terminating).
const maxFoldDepth = 6

// partialCompactionMarker prefixes a brief that could not fully summarize the
// history (the fold hit its depth bound), so the loss is visible to the model and
// the developer rather than silent — the full transcript on disk backs it.
const partialCompactionMarker = "[compaction incomplete: the conversation exceeded the fold limit, so only its most recent portion is summarized below — the full transcript is preserved on disk]"

// foldSummarize summarizes text without any single model call exceeding budget: it
// chunks the text, summarizes each chunk, and folds the chunk-summaries until they
// fit, then unifies them into one brief. This is what makes compaction self-safe
// even when the history is itself larger than the context window.
func foldSummarize(ctx context.Context, prov llm.Provider, text string, budget int) (string, error) {
	return foldSummarizeDepth(ctx, prov, text, budget, 0)
}

func foldSummarizeDepth(ctx context.Context, prov llm.Provider, text string, budget, depth int) (string, error) {
	chunkTok := budget * 3 / 4 // leave room for system + instruction + the produced summary
	if chunkTok <= 0 {
		chunkTok = budget
	}
	chunks := splitByTokens(text, chunkTok)
	if len(chunks) == 1 {
		return summarizeOne(ctx, prov, chunks[0])
	}
	if depth >= maxFoldDepth {
		// Summaries aren't converging (a pathological/verbose model). Stop folding and
		// summarize the MOST RECENT chunk (recency beats the head), MARKED partial so
		// the dropped content is observable — never silently lost — and backed by the
		// full transcript on disk.
		tail, err := summarizeOne(ctx, prov, chunks[len(chunks)-1])
		if err != nil {
			return "", err
		}
		return partialCompactionMarker + "\n\n" + tail, nil
	}
	parts := make([]string, 0, len(chunks))
	for _, c := range chunks {
		s, err := summarizeOne(ctx, prov, c)
		if err != nil {
			return "", err
		}
		parts = append(parts, s)
	}
	combined := strings.Join(parts, "\n\n")
	if llm.EstimateTokens(llm.ChatRequest{Messages: []llm.Message{llm.TextMessage(llm.RoleUser, combined)}}) > chunkTok {
		return foldSummarizeDepth(ctx, prov, combined, budget, depth+1) // still too big → fold again
	}
	return summarizeOne(ctx, prov, combined) // unify the chunk summaries into one brief
}

// writeTranscript persists the full transcript to dir as a canonical record
// (best-effort; a write miss never fails compaction). Returns the written path.
func writeTranscript(dir, transcript string) string {
	if dir == "" {
		return ""
	}
	// A real content hash (not shortHash, which truncates to 8 chars and collides on
	// the constant "[user] " prefix): distinct transcripts get distinct files, so a
	// later compaction never clobbers an earlier canonical record.
	sum := sha256.Sum256([]byte(transcript))
	path := filepath.Join(dir, fmt.Sprintf("transcript-%x.md", sum[:8]))
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		return ""
	}
	return path
}

// compactSession replaces a session's history with a self-safe, model-written
// brief, after persisting the full transcript to disk. Returns how many messages
// were compacted and the transcript path. No-ops (count 0) on an empty session,
// no provider, or an empty summary.
func (a *OrionAgent) compactSession(ctx context.Context, sessionID string) (int, string, error) {
	a.mu.Lock()
	msgs := append([]llm.Message(nil), a.sessions[sessionID]...)
	prov := a.provider
	a.mu.Unlock()
	if len(msgs) == 0 || prov == nil {
		return 0, "", nil
	}

	transcript := renderTranscript(msgs)
	var dir string
	if st := a.conductor.Store(); st != nil {
		dir = st.Dir()
	}
	path := writeTranscript(dir, transcript)

	summary, err := foldSummarize(ctx, prov, transcript, compactBudget(prov))
	if err != nil {
		return 0, "", err
	}
	if summary == "" {
		return 0, "", nil
	}
	// The summarizer is instructed to preserve ratified-spec details, and the spec +
	// decisions are durable in the Context Store — recallable via the spec tools
	// (recall is model-initiated, not auto-injected each turn) — with the full
	// transcript on disk as a backstop. So the lossy brief need not carry everything.
	note := "[Summary of the earlier conversation]\n" + summary
	if path != "" {
		note += "\n\n[Full transcript preserved at " + path + "]"
	}
	a.mu.Lock()
	a.sessions[sessionID] = []llm.Message{llm.TextMessage(llm.RoleUser, note)}
	a.mu.Unlock()
	return len(msgs), path, nil
}

// persistSession writes a human-readable transcript of the session to disk after
// each turn (best-effort — a write miss never affects the turn), so a session that
// dies or misbehaves is recoverable and inspectable. The in-memory sessions map is
// otherwise lost on exit. Transcripts live under <store>/sessions/, one file per
// session named by its start time so they're findable later; each turn overwrites
// it with the latest state, written atomically (temp + rename).
func (a *OrionAgent) persistSession(sessionID string, convo []llm.Message) {
	st := a.conductor.Store()
	if st == nil || len(convo) == 0 {
		return
	}
	dir := filepath.Join(st.Dir(), "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	name := a.sessionStamp(sessionID) + ".md"
	tmp := filepath.Join(dir, "."+name+".tmp")
	if err := os.WriteFile(tmp, []byte(renderTranscript(convo)), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, filepath.Join(dir, name)) // atomic replace
}

// sessionStamp is the transcript filename stem for a session: its start time
// (recorded on first sight, stable across turns) + a sanitized id for uniqueness,
// so files sort chronologically and two sessions never collide.
func (a *OrionAgent) sessionStamp(sessionID string) string {
	a.mu.Lock()
	t, ok := a.starts[sessionID]
	if !ok {
		t = time.Now()
		a.starts[sessionID] = t
	}
	a.mu.Unlock()
	return t.Format("20060102T150405") + "_" + sanitizeID(sessionID)
}

// sanitizeID makes a session id safe as a filename component.
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}

// dialogueDominates gates PROACTIVE compaction. Compaction only shrinks the
// MESSAGE history — it never touches the system prompt or tool specs (a fixed
// floor) — so the gate measures the REDUCIBLE message portion (after clearing
// re-fetchable tool output), NOT the floor. This is what makes it fire only when
// summarizing dialogue actually helps: it never spuriously compacts every turn
// just because the tool/system floor is large on a small window (that's a
// window-too-small condition compaction can't fix), and it never fires on a
// tool-heavy convo the harness would simply clear.
func dialogueDominates(convo []llm.Message, prov llm.Provider) bool {
	policy := contextwindow.For(contextwindow.WindowOf(prov, contextwindow.DefaultWindow))
	cleared := contextwindow.Fit(llm.ChatRequest{Messages: convo}, 0, 0) // clear ALL tool-result bodies
	return llm.EstimateTokens(llm.ChatRequest{Messages: cleared}) > policy.CompactAt
}

// fitsWindow reports whether a request would sit under the hard GuardAt margin
// (below the window, with room for the response) — used by the reactive path to
// avoid re-sending a prompt (e.g. a single giant user message) that compaction
// can't shrink.
func fitsWindow(system string, convo []llm.Message, tools []llm.Tool, prov llm.Provider) bool {
	policy := contextwindow.For(contextwindow.WindowOf(prov, contextwindow.DefaultWindow))
	req := llm.ChatRequest{System: system, Messages: convo, Tools: tools}
	est := llm.EstimateTokens(req)
	// or-hhq cost-aware exact counting: the chars/4 estimate decides when it
	// is CLEARLY on one side of the guard; only the ambiguous band (±20%,
	// wider than the 15% GuardAt margin the estimate/exact gap can eat) pays
	// for the provider's exact count.
	switch {
	case float64(est) <= 0.8*float64(policy.GuardAt):
		return true
	case float64(est) > 1.2*float64(policy.GuardAt):
		return false
	}
	return llm.CountOrEstimate(context.Background(), prov, req) <= policy.GuardAt
}
