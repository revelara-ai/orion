package conductor

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// TestBashDataDirRefusal (or-355i): a bash command that would delete, move, or
// truncate the Orion data store is refused — with a message that also forbids
// delegating it to the developer. Reads of the data dir stay allowed.
func TestBashDataDirRefusal(t *testing.T) {
	dd := "/home/dev/.orion"
	refused := []string{
		"rm -f /home/dev/.orion/orion.db",
		"rm -rf ~/.orion",
		"rm -f $HOME/.orion/orion.db",
		"mv /home/dev/.orion/orion.db /tmp/x",
		"truncate -s 0 /home/dev/.orion/orion.db",
		"sqlite3 x && rm /home/dev/.orion/orion.db",
		"cat /dev/null > ~/.orion/orion.db",
	}
	for _, c := range refused {
		err := bashDataDirRefusal(c, dd)
		if err == nil {
			t.Errorf("must refuse a destructive data-store command: %q", c)
			continue
		}
		if !strings.Contains(err.Error(), "developer") {
			t.Errorf("the refusal must forbid delegating to the developer: %q -> %v", c, err)
		}
	}
	allowed := []string{
		"cat ~/.orion/config.yaml",
		`sqlite3 /home/dev/.orion/orion.db "SELECT count(*) FROM projects"`,
		"ls -la ~/.orion",
		"go test ./...",
		"rm -rf ./build", // a workspace path, not the data dir
	}
	for _, c := range allowed {
		if err := bashDataDirRefusal(c, dd); err != nil {
			t.Errorf("must allow a non-destructive / non-data-dir command: %q -> %v", c, err)
		}
	}
	// No data dir resolved → never blocks (fail-open, not fail-closed on infra).
	if err := bashDataDirRefusal("rm -rf ~/.orion", ""); err != nil {
		t.Fatalf("an unknown data dir must not block: %v", err)
	}
}

// TestDataDirWriteRefusal (or-355i): write_file/edit_file into the data store are
// refused regardless of the workspace anchor — the anchor blocked ~/.orion only
// incidentally (it's outside the workspace); this guard is explicit and holds
// even under ORION_WORKSPACE_WRITES=unrestricted.
func TestDataDirWriteRefusal(t *testing.T) {
	dd := "/home/dev/.orion"
	for _, target := range []string{
		"/home/dev/.orion/orion.db",
		"/home/dev/.orion",
		"/home/dev/.orion/memory/vec.db",
	} {
		if err := dataDirWriteRefusal(dd, target); err == nil {
			t.Errorf("must refuse a write into the data store: %q", target)
		} else if !strings.Contains(err.Error(), "developer") {
			t.Errorf("the refusal must forbid delegating: %q -> %v", target, err)
		}
	}
	for _, target := range []string{
		"/home/dev/project/main.go",
		"/home/dev/.orionade/x", // a sibling that merely shares a prefix substring
	} {
		if err := dataDirWriteRefusal(dd, target); err != nil {
			t.Errorf("must allow a write outside the data store: %q -> %v", target, err)
		}
	}
}

// TestBashToolWiresDataDirGuard (or-355i): the live bash tool refuses a data-store
// deletion BEFORE executing it, even with the red button clear — the guard the
// dogfood run lacked.
func TestBashToolWiresDataDirGuard(t *testing.T) {
	c := orchestrator.NewWithStore(openStore(t))
	dd := c.Store().Dir() // the test store's dir stands in for ~/.orion
	r := specTools(c, nil, &changeSession{}, nil)
	tool, ok := r.Get("bash")
	if !ok {
		t.Fatal("bash not registered")
	}
	cmd := "rm -rf " + filepath.Join(dd, "orion.db")
	out, err := tool.Run(context.Background(), json.RawMessage(`{"command":`+jsonString(cmd)+`}`))
	if err == nil {
		t.Fatalf("bash must refuse deleting the data store, got output: %q", out)
	}
	if !strings.Contains(err.Error(), "data store") {
		t.Fatalf("the refusal must name the data store: %v", err)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
