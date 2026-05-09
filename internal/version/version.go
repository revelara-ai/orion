// Package version exposes build-time identification for orion binaries.
//
// Values are overridable at link time via:
//
//	go build -ldflags "-X github.com/revelara-ai/orion/internal/version.Version=v0.1.0 \
//	                  -X github.com/revelara-ai/orion/internal/version.Commit=$(git rev-parse HEAD) \
//	                  -X github.com/revelara-ai/orion/internal/version.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
package version

import "fmt"

// Set via -ldflags at build time. Defaults are used for raw `go run` invocations.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// String returns a single-line human-readable version banner.
func String() string {
	return fmt.Sprintf("orion %s (commit %s, built %s)", Version, Commit, BuildDate)
}
