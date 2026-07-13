package llm_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPkgHasNoInternalDeps enforces the publishable-module boundary: nothing
// under pkg/ may depend on internal/ (spec: extraction to its own repo must
// stay mechanical). ONE deliberate exception: pkg/orionsdk IS the sanctioned
// facade over the internal engine (or-ykz.4) — it embeds Orion in-process
// and can never extract to its own repo; every other pkg/ package must.
func TestPkgHasNoInternalDeps(t *testing.T) {
	list := func(pattern string) []string {
		out, err := exec.Command("go", "list", "-deps", "-test", pattern).Output()
		if err != nil {
			t.Fatalf("go list %s: %v", pattern, err)
		}
		return strings.Fields(string(out))
	}
	for _, pattern := range []string{
		"github.com/revelara-ai/orion/pkg/llm/...",
		"github.com/revelara-ai/orion/pkg/llmclient",
	} {
		for _, dep := range list(pattern) {
			if strings.Contains(dep, "revelara-ai/orion/internal") {
				t.Errorf("%s depends on internal package %s", pattern, dep)
			}
		}
	}
}
