package conductor

import (
	"strings"

	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/internal/repo"
)

// brownfieldIntake derives the managed-repo intake from a target repo path
// (or-any.8). An empty target is greenfield (the default — BuildDAG inits a fresh
// managed repo). A non-empty target that internal/brownfield classifies as having
// existing source or history is cloned (brownfield); anything else falls back to
// greenfield. This is the wiring that lets BuildDAG build AGAINST an existing repo
// instead of always starting fresh.
func brownfieldIntake(target string) repo.Intake {
	target = strings.TrimSpace(target)
	if target == "" {
		return repo.Intake{}
	}
	if brownfield.Classify(target).Mode == brownfield.Brownfield {
		return repo.Intake{Brownfield: true, Source: target}
	}
	return repo.Intake{}
}
