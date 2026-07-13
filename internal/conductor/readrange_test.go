package conductor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
)

// TestSliceLines (or-mvr.14 lever 3): ranged reads return exactly the asked
// slice with an explicit range marker; no range returns the file untouched;
// an out-of-range start says so instead of returning silent emptiness.
func TestSliceLines(t *testing.T) {
	content := "l1\nl2\nl3\nl4\nl5"
	cases := []struct {
		name              string
		start, count      int
		want, wantMissing string
	}{
		{"no range is identity", 0, 0, content, "[lines"},
		{"middle range", 2, 2, "[lines 2-3 of 5]\nl2\nl3", ""},
		{"range past EOF clips", 4, 10, "[lines 4-5 of 5]\nl4\nl5", ""},
		{"start only reads to end", 3, 0, "[lines 3-5 of 5]\nl3\nl4\nl5", ""},
		{"start beyond EOF says so", 9, 2, "[range starts at line 9 but the file has only 5 lines]", ""},
	}
	for _, tc := range cases {
		got := sliceLines(content, tc.start, tc.count)
		if got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
		if tc.wantMissing != "" && strings.Contains(got, tc.wantMissing) {
			t.Fatalf("%s: %q must not appear in %q", tc.name, tc.wantMissing, got)
		}
	}
}

// TestDiffgenReadFileHonorsRange (or-mvr.14): the diff generator's read_file
// actually threads the range params — the wiring, not just the helper.
func TestDiffgenReadFileHonorsRange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte("a\nb\nc\nd"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := readFileTool(dir)
	out, err := tool.Run(context.Background(), json.RawMessage(`{"path":"f.go","start_line":2,"line_count":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[lines 2-3 of 4]") || !strings.Contains(out, "b\nc") || strings.Contains(out, "a\n") {
		t.Fatalf("ranged read must return exactly lines 2-3, got: %q", out)
	}
}

// TestWorkspaceReadFileHonorsRange (or-mvr.14): the conductor workspace
// read_file threads the range params too.
func TestWorkspaceReadFileHonorsRange(t *testing.T) {
	reg := tools.NewRegistry()
	registerWorkspaceTools(reg, nil)
	rf, ok := reg.Get("read_file")
	if !ok {
		t.Fatal("read_file not registered")
	}
	p := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(p, []byte("a\nb\nc\nd"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := rf.Run(context.Background(), json.RawMessage(`{"path":"`+p+`","start_line":3,"line_count":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[lines 3-3 of 4]") || !strings.Contains(out, "c") {
		t.Fatalf("ranged read must return line 3, got: %q", out)
	}
}
