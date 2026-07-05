package conductor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ejson(t *testing.T, m map[string]string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestEditFileToolLargeFileSurgical (or-5sj): a surgical edit_file changes ONLY the
// target span of a large (>128KB) file — the emitted payload is O(change), not
// O(file), so unlike a full-file write_file it cannot truncate on big files. The
// whole file except the edited span must remain byte-identical.
func TestEditFileToolLargeFileSurgical(t *testing.T) {
	dir := t.TempDir()
	// Build a >128KB file with a single unique marker buried in the middle.
	filler := strings.Repeat("// line of unrelated code that must survive untouched\n", 4000) // ~200KB
	head := filler[:len(filler)/2]
	tail := filler[len(filler)/2:]
	const marker = "\tresult := computeOld(x) // UNIQUE-EDIT-TARGET\n"
	original := head + marker + tail
	if len(original) <= 128<<10 {
		t.Fatalf("fixture must exceed 128KB, got %d bytes", len(original))
	}
	fp := filepath.Join(dir, "big.go")
	if err := os.WriteFile(fp, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	const replacement = "\tresult := computeNew(x, y) // UNIQUE-EDIT-TARGET\n"
	tool := editFileTool(dir)
	out, err := tool.Run(context.Background(), ejson(t, map[string]string{
		"path": "big.go", "old_string": marker, "new_string": replacement,
	}))
	if err != nil {
		t.Fatalf("edit_file on large file: %v", err)
	}
	if !strings.Contains(out, "big.go") {
		t.Errorf("result should name the edited file, got %q", out)
	}

	got, err := os.ReadFile(fp) // #nosec G304 -- test reads a fixture it just wrote under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	want := head + replacement + tail
	if !bytes.Equal(got, []byte(want)) {
		t.Fatalf("large-file edit corrupted the file: got %d bytes, want %d bytes", len(got), len(want))
	}
	// The head and tail regions must be byte-identical (no truncation / no drift).
	if !bytes.HasPrefix(got, []byte(head)) {
		t.Error("region before the edit was not byte-identical")
	}
	if !bytes.HasSuffix(got, []byte(tail)) {
		t.Error("region after the edit was not byte-identical (possible truncation)")
	}
	if bytes.Contains(got, []byte(marker)) {
		t.Error("old_string should no longer be present after the edit")
	}
}

// TestEditFileToolUniqueMatchAndGuards (or-5sj): edit_file rejects an ambiguous
// (zero or multiple) match with a clear error, refuses to escape the sandbox root,
// and errors on a missing file — so an untrusted generator can never apply a
// silent or out-of-bounds edit.
func TestEditFileToolUniqueMatchAndGuards(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "f.go")
	if err := os.WriteFile(fp, []byte("alpha beta alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := editFileTool(dir)
	ctx := context.Background()

	// zero matches -> error
	if _, err := tool.Run(ctx, ejson(t, map[string]string{"path": "f.go", "old_string": "gamma", "new_string": "x"})); err == nil {
		t.Error("edit_file should reject an old_string with zero matches")
	}
	// multiple matches -> error mentioning non-uniqueness, and the file is untouched
	if _, err := tool.Run(ctx, ejson(t, map[string]string{"path": "f.go", "old_string": "alpha", "new_string": "x"})); err == nil {
		t.Error("edit_file should reject a non-unique old_string")
	} else if !strings.Contains(err.Error(), "unique") {
		t.Errorf("non-unique error should say so, got %v", err)
	}
	if b, _ := os.ReadFile(fp); string(b) != "alpha beta alpha\n" { // #nosec G304 -- test reads a fixture under t.TempDir()
		t.Errorf("file must be untouched after a rejected edit, got %q", b)
	}
	// unique match -> applied
	if _, err := tool.Run(ctx, ejson(t, map[string]string{"path": "f.go", "old_string": "beta", "new_string": "BETA"})); err != nil {
		t.Fatalf("unique edit should succeed: %v", err)
	}
	if b, _ := os.ReadFile(fp); string(b) != "alpha BETA alpha\n" { // #nosec G304 -- test reads a fixture under t.TempDir()
		t.Errorf("unique edit not applied: %q", b)
	}
	// path escape -> error
	if _, err := tool.Run(ctx, ejson(t, map[string]string{"path": "../escape.go", "old_string": "a", "new_string": "b"})); err == nil {
		t.Error("edit_file must reject a path escaping the sandbox root")
	}
	// missing file -> error (edit_file does not create files; write_file does)
	if _, err := tool.Run(ctx, ejson(t, map[string]string{"path": "nope.go", "old_string": "a", "new_string": "b"})); err == nil {
		t.Error("edit_file should error on a missing file")
	}
}
