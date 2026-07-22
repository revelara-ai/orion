package conductor

import (
	"fmt"
	"os/exec"
	"strings"
)

// lookPathFn is exec.LookPath, injectable for tests.
var lookPathFn = exec.LookPath

// generationTools maps intent keywords to the GENERATION-time tool they imply.
// Proof-time capability is covered by the manifest (proofexec); this preflight
// catches the generation side, where the tool must exist on the HOST.
var generationTools = []struct {
	keywords []string
	tool     string
	hint     string
}{
	{
		keywords: []string{"grpc", "protobuf", "proto3", ".proto", "protoc"},
		tool:     "protoc",
		hint:     "run `orion doctor` or accept the preflight install offer; generated .pb.go is then committed as source",
	},
}

// capabilityGaps preflights a change intent against generation-time tool
// availability (or-fvkm): an intent implying a tool the host lacks surfaces
// the gap BEFORE any generation attempt burns a budget against it. Returned
// strings are developer-facing gap descriptions; empty means no known gaps.
func capabilityGaps(intent string) []string {
	low := strings.ToLower(intent)
	var gaps []string
	for _, g := range generationTools {
		for _, kw := range g.keywords {
			if strings.Contains(low, kw) {
				if _, err := lookPathFn(g.tool); err != nil {
					gaps = append(gaps, fmt.Sprintf("%s is not installed on this host, and the intent appears to need it at generation time (%s)", g.tool, g.hint))
				}
				break
			}
		}
	}
	return gaps
}

// featureLikeIntent reports whether a change intent reads like NEW behavior
// (or-fr0d): a regression-only oracle cannot prove a feature — a no-op
// implementation passes it — so feature-shaped intents need either behavioral
// cases or the developer's explicit regression-only agreement. Conservative
// keyword scan; false negatives fall to the developer's card review.
func featureLikeIntent(intent string) bool {
	low := strings.ToLower(intent)
	for _, w := range []string{"add ", "adds ", "support", "implement", "introduce", "new ", "feature", "create ", "build ", "expose "} {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}
