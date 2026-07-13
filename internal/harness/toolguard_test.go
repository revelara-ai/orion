package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

func toolsRegistryWithHostileTool() *tools.Registry {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name: "hostile", Description: "returns a huge binary blob",
		Run: func(context.Context, json.RawMessage) (string, error) {
			return "ELF\x00\x01" + strings.Repeat("\x00\xffgarbage", 2_000_000/9), nil
		},
	})
	return reg
}

// TestSanitizeCleanOutputPassesThrough: normal tool output is untouched.
func TestSanitizeCleanOutputPassesThrough(t *testing.T) {
	in := "ok\t42 lines of ordinary build output\nwith unicode: héllo ✓"
	out, quarantined := sanitizeToolResult(in)
	if quarantined || out != in {
		t.Fatalf("clean output must pass through, got quarantined=%v out=%q", quarantined, out)
	}
}

// TestSanitizeQuarantinesBinary (or-mvr.7): binary output is withheld, not fed
// to the model — one bad payload must never crash or pollute the loop.
func TestSanitizeQuarantinesBinary(t *testing.T) {
	in := "PNG\x00\x00\x01\x02" + strings.Repeat("\xff\xfe", 200)
	out, quarantined := sanitizeToolResult(in)
	if !quarantined {
		t.Fatal("binary output must be quarantined")
	}
	if strings.ContainsRune(out, 0) || !utf8.ValidString(out) {
		t.Fatalf("the quarantine descriptor itself must be clean text, got %q", out)
	}
	if !strings.Contains(out, "binary") || !strings.Contains(out, "withheld") {
		t.Fatalf("the descriptor must say WHAT happened, got %q", out)
	}
}

// TestSanitizeTruncatesOversized: a huge result is bounded, keeping head and
// tail (errors live at the end) with an explicit elision marker.
func TestSanitizeTruncatesOversized(t *testing.T) {
	head := "HEAD-MARKER " + strings.Repeat("a", maxToolResultBytes/2)
	tail := strings.Repeat("b", maxToolResultBytes/2) + " TAIL-MARKER"
	out, _ := sanitizeToolResult(head + strings.Repeat("x", maxToolResultBytes*3) + tail)
	if len(out) > maxToolResultBytes+512 {
		t.Fatalf("sanitized output must be bounded, got %d bytes", len(out))
	}
	if !strings.Contains(out, "HEAD-MARKER") || !strings.Contains(out, "TAIL-MARKER") {
		t.Fatal("truncation must keep the head AND the tail")
	}
	if !strings.Contains(out, "truncated") {
		t.Fatal("truncation must be explicit, never silent")
	}
	if !utf8.ValidString(out) {
		t.Fatal("truncation must not split a multi-byte rune")
	}
}

// TestSanitizeRepairsInvalidUTF8: texty output with stray invalid bytes is
// repaired (the inc-u12 parser-crash class), not dropped.
func TestSanitizeRepairsInvalidUTF8(t *testing.T) {
	out, quarantined := sanitizeToolResult("mostly fine \x80\x81 text")
	if quarantined {
		t.Fatal("texty output with stray bad bytes should be repaired, not quarantined")
	}
	if !utf8.ValidString(out) || !strings.Contains(out, "mostly fine") {
		t.Fatalf("repair must yield valid UTF-8 preserving the text, got %q", out)
	}
}

// FuzzSanitizeToolResult (or-mvr.7 acceptance): NO input may crash the
// boundary, and every output is bounded valid UTF-8.
func FuzzSanitizeToolResult(f *testing.F) {
	f.Add("plain")
	f.Add("bin\x00ary")
	f.Add(strings.Repeat("z", maxToolResultBytes*2))
	f.Add("bad\xff\xfeutf8")
	f.Fuzz(func(t *testing.T, in string) {
		out, _ := sanitizeToolResult(in)
		if !utf8.ValidString(out) {
			t.Fatalf("output must always be valid UTF-8")
		}
		if len(out) > maxToolResultBytes+512 {
			t.Fatalf("output must always be bounded, got %d", len(out))
		}
	})
}

// TestRunSurvivesHostileToolOutput (or-mvr.7 acceptance, loop level): a tool
// returning a huge binary payload cannot crash Run or enter the conversation
// unbounded — the fed-back tool_result is the quarantine descriptor.
func TestRunSurvivesHostileToolOutput(t *testing.T) {
	reg := toolsRegistryWithHostileTool()
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		toolUseResp("t1", "hostile", `{}`),
		endResp("done"),
	}}
	loop := Loop{Provider: prov, Tools: reg, System: "s"}
	convo, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err != nil {
		t.Fatalf("hostile tool output must not fail the loop: %v", err)
	}
	var result string
	for _, m := range convo {
		for _, b := range m.Content {
			if b.Type == llm.BlockToolResult {
				result = b.ToolResult.Content
			}
		}
	}
	if len(result) > maxToolResultBytes+512 {
		t.Fatalf("fed-back result must be bounded, got %d bytes", len(result))
	}
	if !utf8.ValidString(result) || !strings.Contains(result, "withheld") {
		t.Fatalf("the result must be the clean quarantine descriptor, got %.120q", result)
	}
}
