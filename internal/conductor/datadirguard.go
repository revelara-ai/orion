package conductor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// The Orion data store (~/.orion — the context DB, memory, credentials) is the
// durable source of truth. A dogfooding agent, hitting an internal schema bug,
// tried to rm the database, then routed around the denial by asking the
// developer to delete it manually (or-355i). These guards make destructive
// operations on the data store a HARD refusal in the tool layer — independent of
// the workspace-write anchor, which only blocked it incidentally (it lives
// outside the workspace) — and the refusals forbid delegating the act to the
// human. A schema change is a MIGRATION in internal/contextstore, never a reset.

// orionDataDir resolves the protected data directory: the conductor's store dir
// when available, else ~/.orion. "" (unresolvable) makes the guards fail-open —
// they never block work over missing infrastructure.
func orionDataDir(c *orchestrator.Conductor) string {
	if c != nil {
		if st := c.Store(); st != nil {
			if d := strings.TrimSpace(st.Dir()); d != "" {
				return filepath.Clean(d)
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Clean(filepath.Join(home, ".orion"))
	}
	return ""
}

// inDataDir reports whether resolvedAbs is the data dir or a path inside it.
func inDataDir(dataDir, resolvedAbs string) bool {
	return resolvedAbs == dataDir || strings.HasPrefix(resolvedAbs, dataDir+string(os.PathSeparator))
}

// dataDirWriteRefusal refuses a write_file/edit_file whose target resolves into
// the data store — even under ORION_WORKSPACE_WRITES=unrestricted.
func dataDirWriteRefusal(dataDir, target string) error {
	if dataDir == "" {
		return nil
	}
	abs := target
	if !filepath.IsAbs(abs) {
		if wd, err := os.Getwd(); err == nil {
			abs = filepath.Join(wd, abs)
		}
	}
	abs = filepath.Clean(abs)
	if inDataDir(dataDir, abs) {
		return fmt.Errorf("refusing to write %q: it is inside the Orion data store %s, which holds the durable project/spec/proof/memory state and must never be overwritten or emptied by a tool. If a schema change is needed, add a migration in internal/contextstore — never delete or recreate the database. Do NOT ask the developer to perform this write on your behalf either", target, dataDir)
	}
	return nil
}

// fsMutationRe matches filesystem-destructive verbs at command position.
var fsMutationRe = regexp.MustCompile("(?i)(?:^|[;&|`(]\\s*)(rm|rmdir|unlink|shred|truncate|dd|mkfs\\w*|mv)\\b|\\s-delete\\b")

// bashDataDirRefusal refuses a bash command that would delete, move, truncate,
// or redirect-over the data store. It fires only when the command BOTH references
// the data dir AND carries a destructive verb or a redirect INTO it — so reads
// (cat/ls/sqlite SELECT) of the data dir stay allowed.
func bashDataDirRefusal(command, dataDir string) error {
	if dataDir == "" {
		return nil
	}
	base := filepath.Base(dataDir) // ".orion"
	aliases := []string{dataDir, "~/" + base, "$HOME/" + base, "${HOME}/" + base}
	referenced := false
	for _, a := range aliases {
		if strings.Contains(command, a) {
			referenced = true
			break
		}
	}
	if !referenced {
		return nil
	}
	destructive := fsMutationRe.MatchString(command)
	if !destructive {
		// A shell redirect whose target is the data store truncates/overwrites it.
		var q []string
		for _, a := range aliases {
			q = append(q, regexp.QuoteMeta(a))
		}
		redirectInto := regexp.MustCompile(`>\s*['"]?(?:` + strings.Join(q, "|") + `)`)
		destructive = redirectInto.MatchString(command)
	}
	if destructive {
		return fmt.Errorf("refusing this command: it targets the Orion data store (%s), which holds the durable project/spec/proof/memory state. Deleting, moving, truncating, or overwriting it is never the right fix — a schema change is a MIGRATION in internal/contextstore, not a database reset. Do NOT ask the developer to run this on your behalf either", dataDir)
	}
	return nil
}
