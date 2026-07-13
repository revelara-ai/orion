// Package toolingcfg curates a generated golangci-lint config before it is used to PROVE a
// tooling change. A generated .golangci.yml is untrusted: a `custom:`/`plugins:` module-plugin
// key makes golangci-lint compile + load attacker Go in-process. This package statically rejects
// those keys and writes an Orion-controlled copy that verify commands pass via `--config`, so
// the generated file is treated as DATA, never the active control file picked up from the CWD.
// The sandbox (proofexec.RunTool) is the primary containment; this is defense-in-depth plus an
// honest "the config is valid" signal.
package toolingcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// CuratedConfigName is the Orion-controlled filename written inside the workdir.
const CuratedConfigName = ".orion-golangci.yml"

// AUDIT NOTE (or-tf8 M5): re-audit this deny-list whenever golangci-lint is
// upgraded — new releases add plugin-loading / exec-capable config keys
// (custom linters, go plugins, module plugins) that MUST land here before
// the new version is adopted. Consider pinning the supported config version
// range if the schema churns.
// forbiddenKeys are golangci-lint config keys that load or execute external code (module plugins
// / custom linters). Their presence anywhere in the config is rejected.
var forbiddenKeys = map[string]bool{
	"custom": true, "plugins": true, "module-path": true, "modulepath": true,
}

// CurateGolangciConfig reads the generated golangci config at srcPath, rejects any
// plugin/custom-linter key (returns an error — the config is not safe to honor), and writes a
// curated copy to workdir/.orion-golangci.yml, returning that path. Verify commands then run
// `golangci-lint ... --config <returned>` so the curated copy — never the generated file at its
// default CWD location — is the active config.
func CurateGolangciConfig(srcPath, workdir string) (string, error) {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", err
	}
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("toolingcfg: generated golangci config is not valid YAML: %w", err)
	}
	if k := findForbiddenKey(doc); k != "" {
		return "", fmt.Errorf("toolingcfg: generated golangci config declares %q (plugin/custom-linter) — rejected: it would load arbitrary code", k)
	}
	curated := filepath.Join(workdir, CuratedConfigName)
	if err := os.WriteFile(curated, data, 0o644); err != nil {
		return "", err
	}
	return curated, nil
}

// findForbiddenKey walks the decoded YAML and returns the first forbidden key found (anywhere in
// the tree), or "" if none.
func findForbiddenKey(node any) string {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			if forbiddenKeys[strings.ToLower(strings.TrimSpace(k))] {
				return k
			}
			if f := findForbiddenKey(v); f != "" {
				return f
			}
		}
	case []any:
		for _, v := range n {
			if f := findForbiddenKey(v); f != "" {
				return f
			}
		}
	}
	return ""
}
