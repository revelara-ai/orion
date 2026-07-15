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
