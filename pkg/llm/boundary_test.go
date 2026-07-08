package llm_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPkgHasNoInternalDeps enforces the publishable-module boundary: nothing
// under pkg/ may depend on internal/ (spec: extraction to its own repo must
// stay mechanical).
func TestPkgHasNoInternalDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/revelara-ai/orion/pkg/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.Contains(dep, "revelara-ai/orion/internal") {
			t.Errorf("pkg/ depends on internal package %s", dep)
		}
	}
}
