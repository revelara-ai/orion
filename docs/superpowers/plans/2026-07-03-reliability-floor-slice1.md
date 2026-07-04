# Reliability Floor — Slice 1 (log-only tracer) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retrieve reliability signals from the Revelara corpus once during `orion change`, inject them as advisory context into the brownfield generator, and run the mechanizable ones as golangci-lint checks against the changed code — all **log-only** (blocks nothing).

**Architecture:** A new `internal/reliabilityfloor` package owns a `Signal` type, a pluggable `SignalSource` interface (Revelara MCP is impl #1, a `FakeSource` drives tests), and pure functions to render signals as prompt context and split them into mechanizable (golangci-lint) vs advisory. `ChangeAndProve` retrieves once (intent-keyed) before generation, injects `RenderContext` into the unused `diffGenRole` `repoContext` seam, and after the regression gate runs the golangci-lint checks under `safeenv.Build()` (host module cache — NOT the hermetic proofexec), logging results onto `ChangeResult`.

**Tech Stack:** Go, `internal/polaris` MCP client, `internal/proof/safeenv`, golangci-lint **v2**, `internal/proof/newbehavior` (`VerifyCommand` used only as the obligation *data shape*, not its executor).

## Global Constraints

- Package name is `internal/reliabilityfloor` — do NOT reuse `reliabilityscan`/`reliabilitytier` (existing, different concept).
- Slice 1 is **log-only**: nothing here may block a change, fail the regression gate, or write a proof verdict. `proof.Accept` stays the sole right-to-ship.
- The linter run executes under `safeenv.Build()` for `cmd.Env` (never `os.Environ()`), `cmd.Dir = worktree path`. It must NOT route through `newbehavior.proveVerify` / `proofexec.RunTool` (hermetic, no module cache).
- golangci-lint is **v2**: invoke with `run --no-config --default=none --enable=<linter> ...`.
- `SignalSource` must **fail open**: on auth failure / unreachable corpus / parse error it returns `nil, nil` (no signals) and the floor no-ops — never error the change.
- The untrusted generator never calls the corpus; signals reach it only as rendered text.
- TDD: every task writes the failing test first. Commit after each task.

---

### Task 1: `Signal` type + `SignalSource` interface + `FakeSource`

**Files:**
- Create: `internal/reliabilityfloor/signal.go`
- Create: `internal/reliabilityfloor/source_fake.go`
- Test: `internal/reliabilityfloor/signal_test.go`

**Interfaces:**
- Produces:
  - `type CheckKind string` with consts `CheckNone="none"`, `CheckGolangciLint="golangci-lint"`.
  - `type Check struct { Kind CheckKind; Linters []string }`
  - `type Signal struct { ID, Title, Why string; Severity Severity; Source string; Check Check }`
  - `type Severity int` with `SevLow..SevCritical` and `func ParseSeverity(s string) Severity`.
  - `type SignalSource interface { Fetch(ctx context.Context, projectID, query string) ([]Signal, error) }`
  - `type FakeSource struct { Signals []Signal; Err error }` implementing `SignalSource`.

- [ ] **Step 1: Write the failing test**

```go
package reliabilityfloor

import (
	"context"
	"testing"
)

func TestParseSeverity(t *testing.T) {
	cases := map[string]Severity{
		"critical": SevCritical, "CRITICAL": SevCritical,
		"high": SevHigh, "medium": SevMedium, "low": SevLow,
		"": SevLow, "garbage": SevLow,
	}
	for in, want := range cases {
		if got := ParseSeverity(in); got != want {
			t.Errorf("ParseSeverity(%q)=%v want %v", in, got, want)
		}
	}
}

func TestFakeSourceReturnsConfigured(t *testing.T) {
	want := []Signal{{ID: "RC-1", Title: "t", Severity: SevHigh}}
	src := &FakeSource{Signals: want}
	got, err := src.Fetch(context.Background(), "proj", "q")
	if err != nil || len(got) != 1 || got[0].ID != "RC-1" {
		t.Fatalf("Fetch=%v,%v want RC-1", got, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/reliabilityfloor/ -run 'TestParseSeverity|TestFakeSource' -v`
Expected: FAIL — package/types not defined.

- [ ] **Step 3: Write minimal implementation**

`signal.go`:
```go
// Package reliabilityfloor sources reliability signals from a corpus and uses them
// twice: as advisory context for the generator and as log-only golangci-lint checks.
// Distinct from reliabilityscan/reliabilitytier (local static tier classification).
package reliabilityfloor

import (
	"context"
	"strings"
)

type Severity int

const (
	SevLow Severity = iota
	SevMedium
	SevHigh
	SevCritical
)

func ParseSeverity(s string) Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return SevCritical
	case "high":
		return SevHigh
	case "medium":
		return SevMedium
	default:
		return SevLow
	}
}

type CheckKind string

const (
	CheckNone         CheckKind = "none"
	CheckGolangciLint CheckKind = "golangci-lint"
)

type Check struct {
	Kind    CheckKind
	Linters []string
}

type Signal struct {
	ID       string // RC-XXX | R-XXX | incident short_name
	Title    string
	Why      string
	Severity Severity
	Source   string // control | risk | knowledge
	Check    Check
}

// SignalSource fetches raw reliability signals for a project + query. Implementations
// MUST fail open: return (nil, nil) on auth/parse/network failure, never a hard error
// that would abort a change.
type SignalSource interface {
	Fetch(ctx context.Context, projectID, query string) ([]Signal, error)
}
```

`source_fake.go`:
```go
package reliabilityfloor

import "context"

// FakeSource is a deterministic SignalSource for tests (no network).
type FakeSource struct {
	Signals []Signal
	Err     error
}

func (f *FakeSource) Fetch(ctx context.Context, projectID, query string) ([]Signal, error) {
	return f.Signals, f.Err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/reliabilityfloor/ -run 'TestParseSeverity|TestFakeSource' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/reliabilityfloor/signal.go internal/reliabilityfloor/source_fake.go internal/reliabilityfloor/signal_test.go
git commit -m "feat(reliabilityfloor): Signal type + SignalSource interface + FakeSource"
```

---

### Task 2: concern→linter map + `AttachChecks`

**Files:**
- Create: `internal/reliabilityfloor/check.go`
- Test: `internal/reliabilityfloor/check_test.go`

**Interfaces:**
- Consumes: `Signal` (Task 1)
- Produces: `func AttachChecks(sigs []Signal) []Signal` — sets `Check` on each signal by matching its Title/Why against a curated concern→linter table; unmatched signals get `Check{Kind: CheckNone}`.

- [ ] **Step 1: Write the failing test**

```go
package reliabilityfloor

import "testing"

func TestAttachChecksMapsTimeout(t *testing.T) {
	out := AttachChecks([]Signal{{ID: "RC-1", Title: "Outbound HTTP call without timeout"}})
	if out[0].Check.Kind != CheckGolangciLint {
		t.Fatalf("kind=%v want golangci-lint", out[0].Check.Kind)
	}
	if !contains(out[0].Check.Linters, "noctx") {
		t.Fatalf("linters=%v want noctx", out[0].Check.Linters)
	}
}

func TestAttachChecksUnmatchedIsAdvisory(t *testing.T) {
	out := AttachChecks([]Signal{{ID: "R-9", Title: "Establish an on-call rotation"}})
	if out[0].Check.Kind != CheckNone {
		t.Fatalf("kind=%v want none", out[0].Check.Kind)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/reliabilityfloor/ -run TestAttachChecks -v`
Expected: FAIL — `AttachChecks` undefined.

- [ ] **Step 3: Write minimal implementation**

`check.go`:
```go
package reliabilityfloor

import "strings"

// concernLinters maps a reliability concern (matched as a lowercase substring of a
// signal's Title+Why) to the golangci-lint v2 linters that mechanize it. Curated and
// low-false-positive by design; unmatched signals stay advisory (Check.Kind == none).
var concernLinters = []struct {
	keywords []string
	linters  []string
}{
	{[]string{"timeout", "without context", "no context", "deadline"}, []string{"noctx", "contextcheck"}},
	{[]string{"body", "unclosed", "leak", "resource not closed"}, []string{"bodyclose"}},
	{[]string{"sql rows", "rows.err", "sql result"}, []string{"rowserrcheck", "sqlclosecheck"}},
	{[]string{"swallow", "ignored error", "unchecked error"}, []string{"errcheck"}},
	{[]string{"injection", "insecure", "hardcoded credential", "weak crypto"}, []string{"gosec"}},
}

// AttachChecks sets Check on each signal by matching concern keywords; unmatched → none.
func AttachChecks(sigs []Signal) []Signal {
	out := make([]Signal, len(sigs))
	for i, s := range sigs {
		hay := strings.ToLower(s.Title + " " + s.Why)
		s.Check = Check{Kind: CheckNone}
		for _, m := range concernLinters {
			if anySubstr(hay, m.keywords) {
				s.Check = Check{Kind: CheckGolangciLint, Linters: m.linters}
				break
			}
		}
		out[i] = s
	}
	return out
}

func anySubstr(hay string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(hay, n) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/reliabilityfloor/ -run TestAttachChecks -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/reliabilityfloor/check.go internal/reliabilityfloor/check_test.go
git commit -m "feat(reliabilityfloor): concern->golangci-lint mapping (AttachChecks)"
```

---

### Task 3: `Retrieve` — fetch, dedupe, cap, attach checks

**Files:**
- Create: `internal/reliabilityfloor/retrieve.go`
- Test: `internal/reliabilityfloor/retrieve_test.go`

**Interfaces:**
- Consumes: `SignalSource`, `AttachChecks` (Tasks 1–2)
- Produces: `func Retrieve(ctx context.Context, src SignalSource, projectID, intent string, maxN int) []Signal` — calls `src.Fetch`; on error/`nil` returns `nil` (fail open); dedupes by `ID`; sorts by descending `Severity` (stable); caps to `maxN`; returns `AttachChecks(...)` of the result.

- [ ] **Step 1: Write the failing test**

```go
package reliabilityfloor

import (
	"context"
	"errors"
	"testing"
)

func TestRetrieveFailsOpen(t *testing.T) {
	got := Retrieve(context.Background(), &FakeSource{Err: errors.New("no creds")}, "p", "add http client", 3)
	if got != nil {
		t.Fatalf("want nil on source error, got %v", got)
	}
}

func TestRetrieveDedupesSortsCapsAttaches(t *testing.T) {
	src := &FakeSource{Signals: []Signal{
		{ID: "RC-1", Title: "Outbound HTTP without timeout", Severity: SevHigh},
		{ID: "RC-1", Title: "dup", Severity: SevHigh},
		{ID: "R-2", Title: "on-call rotation", Severity: SevCritical},
		{ID: "R-3", Title: "low thing", Severity: SevLow},
	}}
	got := Retrieve(context.Background(), src, "p", "intent", 2)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 (capped)", len(got))
	}
	if got[0].ID != "R-2" {
		t.Fatalf("first=%s want R-2 (highest severity)", got[0].ID)
	}
	// checks attached: the timeout signal is mechanizable
	var found bool
	for _, s := range got {
		if s.ID == "RC-1" && s.Check.Kind == CheckGolangciLint {
			found = true
		}
	}
	if !found {
		t.Fatal("RC-1 should have golangci-lint check attached")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/reliabilityfloor/ -run TestRetrieve -v`
Expected: FAIL — `Retrieve` undefined.

- [ ] **Step 3: Write minimal implementation**

`retrieve.go`:
```go
package reliabilityfloor

import (
	"context"
	"sort"
)

// Retrieve fetches signals for the change intent, deduped by ID, highest-severity
// first, capped to maxN, with checks attached. Fails open: any source error yields nil.
func Retrieve(ctx context.Context, src SignalSource, projectID, intent string, maxN int) []Signal {
	if src == nil {
		return nil
	}
	raw, err := src.Fetch(ctx, projectID, intent)
	if err != nil || len(raw) == 0 {
		return nil
	}
	seen := map[string]bool{}
	deduped := raw[:0:0]
	for _, s := range raw {
		if s.ID == "" || seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		deduped = append(deduped, s)
	}
	sort.SliceStable(deduped, func(i, j int) bool { return deduped[i].Severity > deduped[j].Severity })
	if maxN > 0 && len(deduped) > maxN {
		deduped = deduped[:maxN]
	}
	return AttachChecks(deduped)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/reliabilityfloor/ -run TestRetrieve -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/reliabilityfloor/retrieve.go internal/reliabilityfloor/retrieve_test.go
git commit -m "feat(reliabilityfloor): Retrieve (fail-open, dedupe, severity-sort, cap)"
```

---

### Task 4: `RenderContext` — pure signals → prompt block

**Files:**
- Create: `internal/reliabilityfloor/render.go`
- Test: `internal/reliabilityfloor/render_test.go`

**Interfaces:**
- Consumes: `Signal` (Task 1)
- Produces: `func RenderContext(sigs []Signal) string` — returns `""` for empty input; otherwise a compact markdown block titled `# Reliability floor (org-grounded; advisory)` with one bullet per signal: `- [SEVERITY] Title (ID) — Why`.

- [ ] **Step 1: Write the failing test**

```go
package reliabilityfloor

import (
	"strings"
	"testing"
)

func TestRenderContextEmpty(t *testing.T) {
	if RenderContext(nil) != "" {
		t.Fatal("empty signals must render empty string")
	}
}

func TestRenderContextFormat(t *testing.T) {
	out := RenderContext([]Signal{
		{ID: "RC-1", Title: "Outbound HTTP without timeout", Why: "inc-2024 took prod down", Severity: SevHigh},
	})
	if !strings.Contains(out, "# Reliability floor") {
		t.Fatalf("missing header: %q", out)
	}
	if !strings.Contains(out, "[HIGH] Outbound HTTP without timeout (RC-1) — inc-2024 took prod down") {
		t.Fatalf("bad bullet: %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/reliabilityfloor/ -run TestRenderContext -v`
Expected: FAIL — `RenderContext` undefined.

- [ ] **Step 3: Write minimal implementation**

`render.go`:
```go
package reliabilityfloor

import (
	"fmt"
	"strings"
)

func (s Severity) String() string {
	switch s {
	case SevCritical:
		return "CRITICAL"
	case SevHigh:
		return "HIGH"
	case SevMedium:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// RenderContext renders signals as an advisory prompt block. Pure; "" when empty.
func RenderContext(sigs []Signal) string {
	if len(sigs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Reliability floor (org-grounded; advisory)\n")
	b.WriteString("Honor these where they apply to your change:\n")
	for _, s := range sigs {
		fmt.Fprintf(&b, "- [%s] %s (%s)", s.Severity, s.Title, s.ID)
		if strings.TrimSpace(s.Why) != "" {
			fmt.Fprintf(&b, " — %s", strings.TrimSpace(s.Why))
		}
		b.WriteString("\n")
	}
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/reliabilityfloor/ -run TestRenderContext -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/reliabilityfloor/render.go internal/reliabilityfloor/render_test.go
git commit -m "feat(reliabilityfloor): RenderContext (pure signals->prompt block)"
```

---

### Task 5: `Split` mechanizable vs advisory + golangci-lint command builder

**Files:**
- Create: `internal/reliabilityfloor/obligations.go`
- Test: `internal/reliabilityfloor/obligations_test.go`

**Interfaces:**
- Consumes: `Signal`, `Check` (Task 1)
- Produces:
  - `func Split(sigs []Signal) (mechanizable, advisory []Signal)` — partition on `Check.Kind`.
  - `func LintArgs(sigs []Signal, dirs []string) []string` — the golangci-lint v2 argv (after the binary): `["run","--no-config","--default=none","--enable=<l1>","--enable=<l2>",... , dir/..., ...]`, linters = union of mechanizable signals' `Check.Linters` (deduped, sorted), targets = each dir suffixed `/...`. Returns `nil` if no mechanizable signals or no dirs.

- [ ] **Step 1: Write the failing test**

```go
package reliabilityfloor

import (
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	mech, adv := Split([]Signal{
		{ID: "a", Check: Check{Kind: CheckGolangciLint, Linters: []string{"noctx"}}},
		{ID: "b", Check: Check{Kind: CheckNone}},
	})
	if len(mech) != 1 || mech[0].ID != "a" || len(adv) != 1 || adv[0].ID != "b" {
		t.Fatalf("split wrong: mech=%v adv=%v", mech, adv)
	}
}

func TestLintArgs(t *testing.T) {
	sigs := []Signal{
		{Check: Check{Kind: CheckGolangciLint, Linters: []string{"bodyclose", "noctx"}}},
		{Check: Check{Kind: CheckGolangciLint, Linters: []string{"noctx"}}},
	}
	got := LintArgs(sigs, []string{"internal/foo"})
	want := []string{"run", "--no-config", "--default=none", "--enable=bodyclose", "--enable=noctx", "internal/foo/..."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v want %v", got, want)
	}
}

func TestLintArgsEmpty(t *testing.T) {
	if LintArgs(nil, []string{"x"}) != nil {
		t.Fatal("no mechanizable signals -> nil args")
	}
	if LintArgs([]Signal{{Check: Check{Kind: CheckGolangciLint, Linters: []string{"noctx"}}}}, nil) != nil {
		t.Fatal("no dirs -> nil args")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/reliabilityfloor/ -run 'TestSplit|TestLintArgs' -v`
Expected: FAIL — `Split`/`LintArgs` undefined.

- [ ] **Step 3: Write minimal implementation**

`obligations.go`:
```go
package reliabilityfloor

import "sort"

// Split partitions signals into mechanizable (a golangci-lint check) and advisory.
func Split(sigs []Signal) (mechanizable, advisory []Signal) {
	for _, s := range sigs {
		if s.Check.Kind == CheckGolangciLint && len(s.Check.Linters) > 0 {
			mechanizable = append(mechanizable, s)
		} else {
			advisory = append(advisory, s)
		}
	}
	return
}

// LintArgs builds the golangci-lint v2 argv (after the binary) for the union of
// mechanizable linters over the given package dirs. nil if either side is empty.
func LintArgs(sigs []Signal, dirs []string) []string {
	set := map[string]bool{}
	for _, s := range sigs {
		if s.Check.Kind != CheckGolangciLint {
			continue
		}
		for _, l := range s.Check.Linters {
			set[l] = true
		}
	}
	if len(set) == 0 || len(dirs) == 0 {
		return nil
	}
	linters := make([]string, 0, len(set))
	for l := range set {
		linters = append(linters, l)
	}
	sort.Strings(linters)
	args := []string{"run", "--no-config", "--default=none"}
	for _, l := range linters {
		args = append(args, "--enable="+l)
	}
	for _, d := range dirs {
		args = append(args, d+"/...")
	}
	return args
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/reliabilityfloor/ -run 'TestSplit|TestLintArgs' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/reliabilityfloor/obligations.go internal/reliabilityfloor/obligations_test.go
git commit -m "feat(reliabilityfloor): Split + golangci-lint v2 LintArgs builder"
```

---

### Task 6: safeenv linter runner (log-only)

**Files:**
- Create: `internal/reliabilityfloor/runner.go`
- Test: `internal/reliabilityfloor/runner_test.go`

**Interfaces:**
- Consumes: `LintArgs` (Task 5), `internal/proof/safeenv`
- Produces:
  - `type LintResult struct { Ran bool; ExitOK bool; Output string; Skipped string }`
  - `func RunLint(ctx context.Context, dir string, args []string) LintResult` — if `args` is nil, returns `LintResult{Skipped: "no mechanizable signals"}`; if the `golangci-lint` binary is absent from PATH, returns `LintResult{Skipped: "golangci-lint not installed"}`; else runs `golangci-lint <args...>` with `cmd.Dir = dir`, `cmd.Env = safeenv.Build()`, capturing combined output; `ExitOK = (err == nil)`. NEVER returns an error — it is log-only.
  - `func GoDirs(changedFiles []string) []string` — the deduped, sorted set of dirs of changed `.go` files (mirrors the existing tier-scan filter).

- [ ] **Step 1: Write the failing test**

```go
package reliabilityfloor

import (
	"context"
	"reflect"
	"testing"
)

func TestGoDirs(t *testing.T) {
	got := GoDirs([]string{"internal/a/x.go", "internal/a/y.go", "README.md", "cmd/z/main.go"})
	want := []string{"cmd/z", "internal/a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GoDirs=%v want %v", got, want)
	}
}

func TestRunLintNilArgsSkips(t *testing.T) {
	r := RunLint(context.Background(), t.TempDir(), nil)
	if r.Ran || r.Skipped == "" {
		t.Fatalf("nil args must skip, got %+v", r)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/reliabilityfloor/ -run 'TestGoDirs|TestRunLintNilArgs' -v`
Expected: FAIL — `GoDirs`/`RunLint` undefined.

- [ ] **Step 3: Write minimal implementation**

`runner.go`:
```go
package reliabilityfloor

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/safeenv"
)

type LintResult struct {
	Ran     bool
	ExitOK  bool
	Output  string
	Skipped string
}

// GoDirs returns the deduped, sorted dirs of changed .go files.
func GoDirs(changedFiles []string) []string {
	set := map[string]bool{}
	for _, f := range changedFiles {
		if strings.HasSuffix(f, ".go") {
			set[filepath.ToSlash(filepath.Dir(f))] = true
		}
	}
	dirs := make([]string, 0, len(set))
	for d := range set {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

// RunLint runs golangci-lint under safeenv (host module cache) in dir. Log-only:
// it never errors and never blocks; a missing binary or nil args is a Skip.
func RunLint(ctx context.Context, dir string, args []string) LintResult {
	if len(args) == 0 {
		return LintResult{Skipped: "no mechanizable signals"}
	}
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		return LintResult{Skipped: "golangci-lint not installed"}
	}
	cmd := exec.CommandContext(ctx, "golangci-lint", args...)
	cmd.Dir = dir
	cmd.Env = safeenv.Build() // host module cache; NEVER os.Environ()
	out, err := cmd.CombinedOutput()
	return LintResult{Ran: true, ExitOK: err == nil, Output: string(out)}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/reliabilityfloor/ -run 'TestGoDirs|TestRunLintNilArgs' -v`
Expected: PASS

- [ ] **Step 5 (optional integration check, skipped if golangci-lint absent): add an exec test**

Append to `runner_test.go`:
```go
func TestRunLintExecutesWhenInstalled(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not installed")
	}
	// This repo is a valid module; running an empty-target set is a Skip, so target this pkg dir.
	r := RunLint(context.Background(), ".", LintArgs(
		[]Signal{{Check: Check{Kind: CheckGolangciLint, Linters: []string{"errcheck"}}}},
		[]string{"."}))
	if !r.Ran {
		t.Fatalf("expected a run, got %+v", r)
	}
}
```
Run: `go test ./internal/reliabilityfloor/ -run TestRunLint -v` (the exec test skips if the binary is absent).

- [ ] **Step 6: Commit**

```bash
git add internal/reliabilityfloor/runner.go internal/reliabilityfloor/runner_test.go
git commit -m "feat(reliabilityfloor): safeenv golangci-lint runner (log-only) + GoDirs"
```

---

### Task 7: `PolarisSource` — MCP adapter (best-effort parse, fail-open)

**Files:**
- Create: `internal/reliabilityfloor/source_polaris.go`
- Test: `internal/reliabilityfloor/source_polaris_test.go`

**Interfaces:**
- Consumes: `internal/polaris` (`Consumer`, `ReliabilityContext`), `SignalSource` (Task 1)
- Produces:
  - `type PolarisSource struct { Consumer *polaris.Consumer }` implementing `SignalSource`.
  - `func parseSignals(rc polaris.ReliabilityContext) []Signal` — best-effort: unmarshal `rc.Controls`, `rc.Risks`, `rc.Knowledge` (each `json.RawMessage`) as `[]map[string]any`, extracting `id`/`short_name`, `title`/`name`, `severity`, `summary`/`description`→`Why`, tagging `Source`. Unknown shapes yield fewer signals, never a panic.
  - `PolarisSource.Fetch` calls `Consumer.Load(ctx, projectID, query)` then `parseSignals`; on error returns `nil, nil` (fail open).

**Note:** the exact corpus JSON shape is not pinned; `parseSignals` is defensive and unit-tested against a representative fixture. Live-corpus behavior is validated in Task 8's dogfood, not here.

- [ ] **Step 1: Write the failing test**

```go
package reliabilityfloor

import (
	"encoding/json"
	"testing"

	"github.com/revelara-ai/orion/internal/polaris"
)

func TestParseSignalsExtractsFields(t *testing.T) {
	rc := polaris.ReliabilityContext{
		Controls: json.RawMessage(`[{"id":"RC-42","title":"HTTP timeout","severity":"high","summary":"inc-9 outage"}]`),
		Risks:    json.RawMessage(`[{"short_name":"R-7","name":"No retries","severity":"medium","description":"flaky dep"}]`),
	}
	got := parseSignals(rc)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2: %+v", len(got), got)
	}
	var rc42 *Signal
	for i := range got {
		if got[i].ID == "RC-42" {
			rc42 = &got[i]
		}
	}
	if rc42 == nil || rc42.Title != "HTTP timeout" || rc42.Severity != SevHigh || rc42.Source != "control" {
		t.Fatalf("RC-42 parsed wrong: %+v", rc42)
	}
}

func TestParseSignalsHandlesGarbage(t *testing.T) {
	got := parseSignals(polaris.ReliabilityContext{Controls: json.RawMessage(`{"not":"an array"}`)})
	if got != nil {
		t.Fatalf("garbage must yield nil, got %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/reliabilityfloor/ -run TestParseSignals -v`
Expected: FAIL — `parseSignals` undefined.

- [ ] **Step 3: Write minimal implementation**

`source_polaris.go`:
```go
package reliabilityfloor

import (
	"context"
	"encoding/json"

	"github.com/revelara-ai/orion/internal/polaris"
)

// PolarisSource fetches signals from the Revelara corpus via the polaris Consumer.
type PolarisSource struct {
	Consumer *polaris.Consumer
}

func (p *PolarisSource) Fetch(ctx context.Context, projectID, query string) ([]Signal, error) {
	if p == nil || p.Consumer == nil {
		return nil, nil // fail open
	}
	rc, err := p.Consumer.Load(ctx, projectID, query)
	if err != nil {
		return nil, nil // fail open
	}
	return parseSignals(rc), nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func parseBucket(raw json.RawMessage, source string, out *[]Signal) {
	if len(raw) == 0 {
		return
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return // best-effort; unknown shape contributes nothing
	}
	for _, it := range items {
		id := firstString(it, "id", "short_name")
		title := firstString(it, "title", "name")
		if id == "" || title == "" {
			continue
		}
		*out = append(*out, Signal{
			ID:       id,
			Title:    title,
			Why:      firstString(it, "summary", "description", "why"),
			Severity: ParseSeverity(firstString(it, "severity")),
			Source:   source,
		})
	}
}

// parseSignals defensively extracts signals from a ReliabilityContext. Never panics.
func parseSignals(rc polaris.ReliabilityContext) []Signal {
	var out []Signal
	parseBucket(rc.Controls, "control", &out)
	parseBucket(rc.Risks, "risk", &out)
	parseBucket(rc.Knowledge, "knowledge", &out)
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/reliabilityfloor/ -run TestParseSignals -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/reliabilityfloor/source_polaris.go internal/reliabilityfloor/source_polaris_test.go
git commit -m "feat(reliabilityfloor): PolarisSource MCP adapter (best-effort, fail-open)"
```

---

### Task 8: Wire the floor into `ChangeAndProve` (log-only)

**Files:**
- Modify: `internal/conductor/changeproof.go` (the `ChangeAndProve` body; `ChangeResult` struct)
- Create: `internal/conductor/reliabilityfloor.go` (the conductor-side glue: build the source, retrieve, log)
- Test: `internal/conductor/reliabilityfloor_test.go`

**Interfaces:**
- Consumes: `reliabilityfloor.{Retrieve,RenderContext,Split,LintArgs,GoDirs,RunLint,PolarisSource}`; existing `mcpClientFromCredentials`, `polaris.NewConsumer`, `changedFiles`.
- Produces (glue, in package `conductor`):
  - `var floorSource func(store *contextstore.Store) reliabilityfloor.SignalSource` — a package var seam, defaulting to a Polaris-backed source, overridable in tests.
  - `func floorSignals(ctx context.Context, store *contextstore.Store, projectID, intent string) []reliabilityfloor.Signal` — builds source via `floorSource`, `Retrieve(..., maxN=5)`.
  - `func runFloorChecks(ctx context.Context, dir string, sigs []reliabilityfloor.Signal, changed []string) reliabilityfloor.LintResult`
  - New `ChangeResult` fields: `FloorSignals []reliabilityfloor.Signal`, `FloorLint reliabilityfloor.LintResult`.

**Wiring points in `ChangeAndProve` (exact):**
- After `res := ChangeResult{...}` and BEFORE the `apply` closure (currently line ~62): `sigs := floorSignals(ctx, store, "", intent); res.FloorSignals = sigs`.
- In the `apply` closure, change the `DiffGenerator` call's `repoContext` arg from `m.Digest()` to `m.Digest() + "\n" + reliabilityfloor.RenderContext(sigs)`.
- After `res.FilesChanged = changedFiles(...)` (currently line ~87): `res.FloorLint = runFloorChecks(ctx, wt.Path, sigs, res.FilesChanged); logFloor(res)` (log-only; do not branch on it).

- [ ] **Step 1: Write the failing test** (uses the `floorSource` seam; no network)

```go
package conductor

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/reliabilityfloor"
)

func TestFloorSignalsUsesSeam(t *testing.T) {
	orig := floorSource
	t.Cleanup(func() { floorSource = orig })
	floorSource = func(_ *contextstore.Store) reliabilityfloor.SignalSource {
		return &reliabilityfloor.FakeSource{Signals: []reliabilityfloor.Signal{
			{ID: "RC-1", Title: "Outbound HTTP without timeout", Severity: reliabilityfloor.SevHigh},
		}}
	}
	got := floorSignals(context.Background(), nil, "", "add an http client call")
	if len(got) != 1 || got[0].ID != "RC-1" {
		t.Fatalf("floorSignals=%v want RC-1", got)
	}
	if got[0].Check.Kind != reliabilityfloor.CheckGolangciLint {
		t.Fatalf("expected check attached, got %v", got[0].Check.Kind)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/conductor/ -run TestFloorSignalsUsesSeam -v`
Expected: FAIL — `floorSource`/`floorSignals` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/conductor/reliabilityfloor.go`:
```go
package conductor

import (
	"context"
	"log"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/polaris"
	"github.com/revelara-ai/orion/internal/reliabilityfloor"
)

// floorSource builds the reliability SignalSource. Overridable in tests.
var floorSource = func(store *contextstore.Store) reliabilityfloor.SignalSource {
	return &reliabilityfloor.PolarisSource{
		Consumer: polaris.NewConsumer(mcpClientFromCredentials(store), store),
	}
}

// floorSignals retrieves up to 5 intent-keyed reliability signals (fail-open).
func floorSignals(ctx context.Context, store *contextstore.Store, projectID, intent string) []reliabilityfloor.Signal {
	return reliabilityfloor.Retrieve(ctx, floorSource(store), projectID, intent, 5)
}

// runFloorChecks runs the mechanizable signals' golangci-lint checks under safeenv,
// log-only, against the changed .go dirs.
func runFloorChecks(ctx context.Context, dir string, sigs []reliabilityfloor.Signal, changed []string) reliabilityfloor.LintResult {
	mech, _ := reliabilityfloor.Split(sigs)
	args := reliabilityfloor.LintArgs(mech, reliabilityfloor.GoDirs(changed))
	return reliabilityfloor.RunLint(ctx, dir, args)
}

func logFloor(res ChangeResult) {
	if len(res.FloorSignals) == 0 {
		log.Printf("reliability floor: no signals")
		return
	}
	log.Printf("reliability floor: %d signals; lint ran=%v exitOK=%v skipped=%q",
		len(res.FloorSignals), res.FloorLint.Ran, res.FloorLint.ExitOK, res.FloorLint.Skipped)
}
```

Then edit `changeproof.go`: add the two `ChangeResult` fields, the three wiring points listed above (retrieve before `apply`; concat `RenderContext` into the `repoContext` arg; `runFloorChecks` + `logFloor` after `changedFiles`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/conductor/ -run TestFloorSignalsUsesSeam -v`
Expected: PASS

- [ ] **Step 5: Full build + package tests**

Run: `go build ./... && go test ./internal/reliabilityfloor/... ./internal/conductor/ -count=1`
Expected: build clean; tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/conductor/reliabilityfloor.go internal/conductor/reliabilityfloor_test.go internal/conductor/changeproof.go
git commit -m "feat(reliabilityfloor): wire log-only floor into ChangeAndProve (retrieve once, use twice)"
```

---

### Task 9: Dogfood — planted missing-timeout `orion change`

**Files:** none (manual acceptance run)

**Acceptance bar for slice 1:** on a real `orion change` against a Go repo, with the corpus reachable, the run (a) retrieves ≥1 reliability signal, (b) injects the rendered block into the generator's context, (c) runs the golangci-lint check under safeenv against the changed dirs, and (d) logs both — with **zero gating** and no change to regression/proof outcomes.

- [ ] **Step 1:** Prepare a throwaway Go repo/worktree with a function that makes an outbound `http.Get` with no timeout/context.
- [ ] **Step 2:** Run `orion change` with an intent that touches that file (credentials present so the Polaris source is live). Capture logs.
- [ ] **Step 3:** Confirm the log shows `reliability floor: N signals; lint ran=true ...` and the generator prompt contained the `# Reliability floor` block (add a temporary debug log of `diffGenRole` output if needed, then remove it).
- [ ] **Step 4:** Confirm the change's delivery verdict is unchanged vs. the same run with the floor disabled (set `floorSource` to return an empty source) — i.e. the floor blocked nothing.
- [ ] **Step 5:** Write findings into the beads epic notes; decide go/no-go for slice 2 (advisory LLM half).

---

## Self-Review

**Spec coverage:** §3 units → Tasks 1,3,4,5 (Signal/SignalSource/Retrieve/RenderContext/Obligations); §5 mechanization map + safeenv lane → Tasks 2,6; `SignalSource` interface + MCP impl → Tasks 1,7; §6 slice-1 (log-only, retrieve-once-use-twice, reversible via the seam) → Task 8; acceptance/dogfood → Task 9. Deferred by design (out of slice 1, in spec §6): `AdvisoryVerify` LLM half, fix-retry, blocking, greenfield wiring, changed-file feature-scan ranking.

**Placeholder scan:** none — every code step shows real code; every run step shows the command + expected result. Task 9 is explicitly a manual acceptance run (no code), not a placeholder.

**Type consistency:** `Signal`, `Check{Kind,Linters}`, `CheckKind`, `Severity`, `SignalSource.Fetch(ctx,projectID,query)`, `Retrieve(ctx,src,projectID,intent,maxN)`, `RenderContext`, `Split`, `LintArgs(sigs,dirs)`, `GoDirs`, `RunLint(ctx,dir,args)`, `LintResult`, `PolarisSource{Consumer}`, `parseSignals(rc)`, and the conductor `floorSource`/`floorSignals`/`runFloorChecks`/`logFloor` names are used identically across tasks.
