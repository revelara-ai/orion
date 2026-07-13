package completeness

import "strings"

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

// provableDirections is the deterministic harness-capability manifest: what
// the decompose→generate→testsynth→prove chain can actually prove TODAY.
// Extending a row (e.g. adding "grpc" when or-4rxw lands) is how capability
// growth reaches the gate. Keys absent here (stack, repo_layout — free-text)
// carry no capability constraint.
var provableDirections = map[string][]string{
	"direction.language":      {"go"},
	"direction.wire_protocol": {"http-json", "text", "http"},
	"direction.engine":        {"none"},
}

// DirectionGaps returns the direction answers that exceed the harness's proof
// capability (empty when everything chosen is provable). Deterministic and
// case-insensitive; non-direction keys are ignored.
func DirectionGaps(answers map[string]string) []Gap {
	var gaps []Gap
	for _, d := range directionDecisions() { // stable order
		provable, constrained := provableDirections[d.Key]
		if !constrained {
			continue
		}
		v := strings.ToLower(strings.TrimSpace(answers[d.Key]))
		if v == "" {
			continue // unanswered = the fallback, which is provable by construction
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
