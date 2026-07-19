package completeness

import (
	"regexp"
	"strings"

	"github.com/revelara-ai/orion/internal/lang"
)

// DimDirection is the development-direction dimension (or-045a.5): stack,
// language, engine, wire protocol, repo layout — the decisions the mech-game
// dogfood made as chat prose (Go assumed until the user pushed back with
// Unreal; gRPC chosen while the plan silently stayed HTTP/JSON). First-class
// ratified decisions ride the answer→dimension→hash→assumption-gate pipeline.
const DimDirection Dimension = "direction"

// directionDecisions are the direction-family questions raised for LARGE-scale
// intakes. Every entry carries a fallback so a standard flow can proceed on
// approved defaults (audited by the assumption gate) — but on a large intake
// they are asked, not assumed.
func directionDecisions() []RequiredDecision {
	return []RequiredDecision{
		{"direction.stack", DimDirection, "Describe the overall stack (client, server, key components) in one line.", "single service"},
		{"direction.language", DimDirection, "Which implementation language/runtime for the part Orion builds?", "go"},
		{"direction.engine", DimDirection, "Is a game/render/simulation engine involved (e.g. Unreal, Unity, Godot)? Which — or none?", "none"},
		{"direction.wire_protocol", DimDirection, "Which wire protocol do clients use (http-json, text, grpc, …)?", "http-json"},
		{"direction.repo_layout", DimDirection, "Where does the code live — the managed repo, a new standalone repo, or an existing one?", "managed-repo"},
	}
}

// NewAnalyzerScaled returns an analyzer whose checklist reflects the intent's
// SCALE as well as its type. The direction family (stack/language/engine/wire/
// repo) is raised whenever the harness cannot presume the stack: for EVERY
// large-scale intake, and for EVERY unregistered project type — a game has no
// functional template, so its language/engine must be elicited rather than
// silently defaulted to Go (or-hn15.4). A standard registered type (the legacy
// http-service path) stays direction-free and byte-compatible with V2, so its
// anchors don't shift. NewAnalyzer (the unscaled constructor) is likewise
// unchanged.
func NewAnalyzerScaled(projectType, scale string) *Analyzer {
	checklist := functionalDecisions(projectType)
	if scale == ScaleLarge || !RegisteredProjectType(projectType) {
		checklist = append(checklist, directionDecisions()...)
	}
	checklist = append(checklist, universalDecisions()...)
	return &Analyzer{projectType: projectType, scale: scale, checklist: checklist}
}

// Gap is one direction decision the proof harness cannot currently prove.
type Gap struct {
	Key      string
	Value    string
	Provable []string // what the harness CAN prove for this key today
}

// staticProvable is the deterministic harness-capability manifest for the
// direction rows Orion proves TODAY. direction.language is NOT here: it is
// sourced from the language registry (see provableFor) so a language is
// "provable" exactly when an adapter is registered — capability can never be
// claimed with nothing behind it (or-4y7.1). Keys absent entirely (stack,
// repo_layout — free-text) carry no capability constraint.
var staticProvable = map[string][]string{
	"direction.wire_protocol": {"http-json", "text", "http"},
	"direction.engine":        {"none"},
}

// provableFor returns the values the harness can prove for a direction key.
// direction.language comes from lang.Registered() (the registry is the
// authority); every other constrained key is static.
func provableFor(key string) ([]string, bool) {
	if key == "direction.language" {
		return lang.Registered(), true
	}
	p, ok := staticProvable[key]
	return p, ok
}

// langRuntimeRE splits a direction.language answer into its base language and
// an optional pinned version: "python 3.12", "python3.12", "Python@3.12.4",
// "go 1.22" all parse; a bare "python"/"go" carries no version.
var langRuntimeRE = regexp.MustCompile(`^([a-z#+]+?)\s*[-@ ]?\s*v?([0-9][0-9.]*)?$`)

// SplitLanguageRuntime normalizes a direction.language answer to (language,
// version) — or-4y7.10: the developer states the runtime they prefer ("python
// 3.12"); the base language drives capability/dispatch and the version becomes
// the direction.runtime pin. Unparseable answers return as-is with no version
// (they then fail the capability gate on their own merits).
func SplitLanguageRuntime(v string) (language, version string) {
	v = strings.ToLower(strings.TrimSpace(v))
	if m := langRuntimeRE.FindStringSubmatch(v); m != nil {
		return m[1], strings.TrimRight(m[2], ".")
	}
	return v, ""
}

// DirectionGaps returns the direction answers that exceed the harness's proof
// capability (empty when everything chosen is provable). Deterministic and
// case-insensitive; non-direction keys are ignored. direction.language answers
// carrying a version pin ("python 3.12") are judged by their BASE language.
func DirectionGaps(answers map[string]string) []Gap {
	var gaps []Gap
	for _, d := range directionDecisions() { // stable order
		provable, constrained := provableFor(d.Key)
		if !constrained {
			continue
		}
		v := strings.ToLower(strings.TrimSpace(answers[d.Key]))
		if v == "" {
			continue // unanswered = the fallback, which is provable by construction
		}
		if d.Key == "direction.language" {
			v, _ = SplitLanguageRuntime(v)
		}
		ok := false
		for _, p := range provable {
			if v == p {
				ok = true
				break
			}
		}
		if !ok {
			gaps = append(gaps, Gap{Key: d.Key, Value: v, Provable: provable})
		}
	}
	return gaps
}
