package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/revelara-ai/orion/internal/budget"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/sandbox"
	"github.com/revelara-ai/orion/internal/tools"
)

// NativeGenerator returns a Generator that produces the program with a model: the
// harness loop, primed with the spec's behavioral contract and a write_file tool,
// writes ARBITRARY code to satisfy the cases. This is what makes Orion general —
// the proof stays the constant; generation is no longer a fixed time-service
// template. The model never sees the proof corpus (it gets the contract — the
// cases — not the harness-authored tests), so it still cannot grade its own
// homework: the (independent) proof holds whatever it writes accountable.
func NativeGenerator(provider llm.Provider, acct *budget.Accountant) Generator {
	return func(ctx context.Context, gs sandbox.GenSpec, buildDir, feedback string) (sandbox.GeneratedArtifact, error) {
		reg := tools.NewRegistry()
		reg.Register(writeFileTool(buildDir))
		loop := harness.Loop{
			Provider:   provider,
			Tools:      reg,
			System:     generationRole(gs),
			Supervisor: harness.Supervisor{MaxIterations: 24, Budget: acct},
		}
		userMsg := "Generate the program now: write each file with write_file (a complete go.mod and main.go), then end your turn."
		if strings.TrimSpace(feedback) != "" {
			// Refinement attempt: hand the model its own prior code + the proof's causal
			// analysis so it FIXES the specific failures instead of regenerating blind.
			// readBuildSource excludes *_test.go, so the harness-authored proof corpus is
			// never exposed — the trust wall holds across refinement.
			userMsg = "Your previous attempt FAILED the independent proof. Here is its causal analysis:\n\n" +
				feedback +
				"\n\nHere is the code you previously wrote:\n\n" + readBuildSource(buildDir) +
				"\n\nFix the failing behavior and rewrite the affected files with write_file (overwrite main.go, and go.mod if needed), then end your turn. " +
				"Address EVERY failing/unexecuted case above, and do not regress the cases that already passed. Write real logic — the proof probes the live service and runs mutation testing."
		}
		start := []llm.Message{llm.TextMessage(llm.RoleUser, userMsg)}
		if _, _, err := loop.Run(ctx, start, nil); err != nil {
			return sandbox.GeneratedArtifact{}, fmt.Errorf("native generation: %w", err)
		}
		return sandbox.ArtifactFromDir(buildDir)
	}
}

// readBuildSource returns the generator's own prior output (go.mod + non-test .go
// files) for a refinement attempt. It EXCLUDES *_test.go: Go requires test files to
// end in _test.go, so this guarantees the harness-authored proof corpus is never
// fed back to the generator (the trust wall — "no agent grades its own homework").
func readBuildSource(buildDir string) string {
	entries, err := os.ReadDir(buildDir)
	if err != nil {
		return "(prior source unavailable)"
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, "_test.go") { // never expose the proof corpus
			continue
		}
		if n == "go.mod" || strings.HasSuffix(n, ".go") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(buildDir, n))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "// ===== %s =====\n%s\n\n", n, string(data))
	}
	if b.Len() == 0 {
		return "(prior source unavailable)"
	}
	return strings.TrimSpace(b.String())
}

// writeFileTool lets the generator write files into the build dir only (a path
// that escapes the dir is rejected — the model's output is untrusted).
func writeFileTool(buildDir string) tools.Tool {
	root := filepath.Clean(buildDir)
	return tools.Tool{
		Name:        "write_file",
		Description: "Write one file of the program. path is relative to the module root (e.g. \"main.go\", \"go.mod\"). Overwrites if it exists. Call once per file.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
		Safety:      tools.Safety{Destructive: true},
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if strings.TrimSpace(p.Path) == "" {
				return "", fmt.Errorf("path is required")
			}
			full := filepath.Join(root, filepath.Clean("/"+p.Path))
			if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
				return "", fmt.Errorf("path %q escapes the build directory", p.Path)
			}
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(full, []byte(p.Content), 0o644); err != nil {
				return "", err
			}
			return "wrote " + p.Path + fmt.Sprintf(" (%d bytes)", len(p.Content)), nil
		},
	}
}

// generationRole primes the model with the behavioral contract. It is
// domain-agnostic: whatever cases the spec carries, the program must satisfy them.
// The handleTime symbol is the proof harness's stable entry point (the behavioral
// mode calls it in-process); the model writes whatever logic satisfies the cases
// behind it.
// GenerationPrompt builds the code generator's system prompt from the spec slice.
// It is CASE-DRIVEN (the declared behavioral cases ARE the contract), uses the
// DECLARED entry symbol, and stresses RELIABILITY — it primes the generator to build
// exactly what the spec requires, not a fixed time-service example. HTTP/service
// details (route, port, format) appear only when the contract carries them; per-case
// requirements (status, content-type, timezone, error shape) ride on the cases.
// writeHint is the protocol-specific file-write instruction so the native and
// spawned-agent paths share ONE general prompt (or-3ba.7 — de-time/HTTP-biased).
func GenerationPrompt(gs sandbox.GenSpec, writeHint string) string {
	module := gs.Module
	if module == "" {
		module = "orion-generated/svc"
	}
	var b strings.Builder
	b.WriteString("You are Orion's code generator. Write COMPLETE, COMPILABLE, RELIABLE Go that satisfies the behavioral contract below — build exactly what the contract requires, nothing more.\n\n")
	b.WriteString("Hard requirements:\n")
	b.WriteString("- A go.mod with `module " + module + "` and a recent `go` line (e.g. go 1.25).\n")
	if gs.ProgramFamily == "library" {
		b.WriteString("- This is a LIBRARY build: create the named packages with the EXPORTED functions/types the cases call (a thin package main { func main() {} } at the root keeps the module buildable). Unit cases call the exported surface directly — signatures must match the case expressions exactly.\n")
	} else if gs.ProgramFamily == "cli" {
		fmt.Fprintf(&b, "- Expose the behavioral entry point as a top-level func `%s(args []string, stdin io.Reader, stdout, stderr io.Writer, env map[string]string) int` — the proof harness calls this symbol directly, and main() MUST be a thin wrapper: `func main() { os.Exit(%s(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, envMap())) }` so the shipped process and the entry behave identically.\n", gs.Entry(), gs.Entry())
	} else {
		fmt.Fprintf(&b, "- Expose the behavioral entry point as a top-level func `%s(w http.ResponseWriter, r *http.Request)` — the proof harness calls this symbol directly.\n", gs.Entry())
	}
	b.WriteString("- Real logic, not hardcoded responses: the proof runs the LIVE program and mutation-tests it; for any input a case specifies (including invalid input) return EXACTLY the status + body that case requires, never crashing.\n")
	b.WriteString("- RELIABILITY (Orion eats its own dog food): server timeouts + graceful shutdown, validated inputs, and errors handled without panicking.\n")
	if gs.Route != "" || gs.Port != 0 {
		fmt.Fprintf(&b, "- A main() that mounts %s and listens on $PORT (default %d), serving route %s as %s.\n", gs.Entry(), portOr(gs.Port), gs.Route, fmtOr(gs.Format, "json"))
	}
	b.WriteString("\nThe program MUST satisfy these behavioral cases (request → expected response) — these ARE the contract:\n")
	cases := append([]spec.BehavioralCase(nil), gs.Cases...)
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	for _, c := range cases {
		b.WriteString(renderCaseForGen(c))
	}
	if len(cases) == 0 {
		b.WriteString("- (no behavioral cases were declared — satisfy the stated intent and the reliability requirements above)\n")
	}
	// or-b73: the trust-tiered recalled context (spec constraints + retrieved memory,
	// generation-tier memory quarantined as data-only) the Conductor assembled.
	if s := strings.TrimSpace(gs.Context); s != "" {
		b.WriteString("\n" + s + "\n")
	}
	b.WriteString("\n" + writeHint)
	return b.String()
}

// generationRole is the native (in-process LLM) generator's system prompt.
func generationRole(gs sandbox.GenSpec) string {
	return GenerationPrompt(gs, "Write go.mod and main.go via write_file, then end your turn.")
}

func renderCaseForGen(c spec.BehavioralCase) string {
	method := c.Request.Method
	if method == "" {
		method = "GET"
	}
	q := ""
	if len(c.Request.Query) > 0 {
		var parts []string
		for k, v := range c.Request.Query {
			parts = append(parts, k+"="+v)
		}
		sort.Strings(parts)
		q = "?" + strings.Join(parts, "&")
	}
	var asserts []string
	for _, a := range c.Expect.Assertions {
		switch a.Kind {
		case spec.AssertJSONKeyPresent:
			asserts = append(asserts, "JSON has non-empty \""+a.Key+"\"")
		case spec.AssertJSONKeyRFC3339:
			asserts = append(asserts, "JSON \""+a.Key+"\" is an RFC3339 timestamp")
		case spec.AssertJSONKeyInTZ:
			asserts = append(asserts, "JSON \""+a.Key+"\" is an RFC3339 timestamp in timezone "+a.Value)
		case spec.AssertJSONErrorPresent:
			asserts = append(asserts, "JSON has a non-empty \"error\"")
		case spec.AssertBodyRFC3339:
			asserts = append(asserts, "body is an RFC3339 timestamp")
		}
	}
	tail := ""
	if len(asserts) > 0 {
		tail = "; " + strings.Join(asserts, "; ")
	}
	return fmt.Sprintf("- %s %s%s → status %d, content-type %s%s\n", method, c.Request.Path, q, c.Expect.Status, c.Expect.ContentType, tail)
}

func fmtOr(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}
func portOr(p int) int {
	if p == 0 {
		return 8080
	}
	return p
}
