package oauth

import "os"

// newDefaultStderr returns os.Stderr typed as stderrWriter. Existing
// to keep the package compileable without depending on os.Stderr's
// concrete type in registry.go (so tests can swap stderr without
// importing os).
func newDefaultStderr() stderrWriter { return os.Stderr }
