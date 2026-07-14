package lang

// goAdapter is the default language — the V2.0 path. In or-4y7.1 it only names
// itself; the polyglot slices add the generation/proof/hazard/export
// capabilities behind their own per-subsystem registries, each keyed "go".
type goAdapter struct{}

func (goAdapter) Language() string { return "go" }

func init() { Register(goAdapter{}) }
