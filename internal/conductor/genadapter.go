package conductor

import (
	"strings"

	"github.com/revelara-ai/orion/internal/sandbox"
)

// genAdapter is a language's native-generation surface (or-4y7.3): the zero-config
// preamble (what to build + the per-family entry contract) and the file-write hint
// that both the native and spawned-agent prompts share. Resolved by
// GenSpec.Language; the Go adapter is the default and byte-identical to V2.0. The
// deterministic fixture and the spawned-agent RenderPrompt fold in with the
// per-language impls (or-4y7.9).
type genAdapter interface {
	Language() string
	// Preamble writes the compiled zero-config generation preamble into b.
	Preamble(b *strings.Builder, gs sandbox.GenSpec, module string)
	// WriteHint is the protocol-specific file-write instruction appended to the
	// prompt (e.g. "Write go.mod and main.go via write_file").
	WriteHint() string
}

var genAdapters = map[string]genAdapter{}

func registerGenAdapter(a genAdapter) { genAdapters[a.Language()] = a }

// genFor resolves the generation adapter for a language ("" → go). An
// unregistered language returns nil (its registration accompanies lang.Registered()).
func genFor(language string) genAdapter {
	if language == "" {
		language = "go"
	}
	return genAdapters[language]
}

// goGen is the default: the V2.0 Go preamble + write-hint, verbatim.
type goGen struct{}

func (goGen) Language() string { return "go" }

func (goGen) Preamble(b *strings.Builder, gs sandbox.GenSpec, module string) {
	writeDefaultPreamble(b, gs, module)
}

func (goGen) WriteHint() string { return "Write go.mod and main.go via write_file, then end your turn." }

func init() { registerGenAdapter(goGen{}) }

// pyGen is Python's generation surface (or-4y7.9, library tracer): the preamble
// states the library contract — an importable package whose exported surface the
// unit cases call directly; no compile step, stdlib only — and the write hint
// names the python files. The reliability posture carries over verbatim: real
// logic (the proof imports and exercises the LIVE module), validated inputs,
// no bare except/pass swallowing.
type pyGen struct{}

func (pyGen) Language() string { return "python" }

func (pyGen) Preamble(b *strings.Builder, _ sandbox.GenSpec, module string) {
	pkg := pyPkgName(module)
	b.WriteString("You are Orion's code generator. Write a COMPLETE, RUNNABLE, RELIABLE Python library that satisfies the behavioral contract below — build exactly what the contract requires, nothing more.\n\n")
	b.WriteString("Hard requirements:\n")
	b.WriteString("- An importable package named `" + pkg + "` (directory with __init__.py) exposing the functions/types the cases call — signatures must match the case expressions exactly.\n")
	b.WriteString("- Standard library only: no third-party imports (the proof sandbox has no package index and no network).\n")
	b.WriteString("- Real logic, not hardcoded returns: the proof imports the LIVE module and calls its surface; for any input a case specifies (including invalid input) behave EXACTLY as that case requires, never crashing the interpreter.\n")
	b.WriteString("- RELIABILITY (Orion eats its own dog food): validate inputs, raise precise exceptions with meaningful messages, and never swallow errors with bare except/pass.\n")
}

func (pyGen) WriteHint() string {
	return "Write the package files (<pkg>/__init__.py and any modules) via write_file, then end your turn."
}

// pyPkgName derives the importable package name from the ratified module path:
// last segment, lowered, dashes/dots flattened to underscores.
func pyPkgName(module string) string {
	seg := module
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	seg = strings.ToLower(seg)
	seg = strings.NewReplacer("-", "_", ".", "_").Replace(seg)
	if seg == "" {
		seg = "artifact"
	}
	return seg
}

func init() { registerGenAdapter(pyGen{}) }
