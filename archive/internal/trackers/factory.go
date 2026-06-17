package trackers

import "fmt"

// FactoryError is returned when no adapter is registered for a kind.
type FactoryError struct {
	Kind TrackerKind
}

func (e *FactoryError) Error() string {
	return fmt.Sprintf("trackers: no adapter registered for kind %q", e.Kind)
}

// factoryFunc constructs an adapter (typically a thin per-kind
// closure that returns the package-level singleton from
// internal/trackers/<kind>).
type factoryFunc func() TrackerAdapter

// factories is the registry. Populated by Register() at init time
// from each adapter subpackage.
var factories = map[TrackerKind]factoryFunc{}

// Register installs an adapter factory under kind. Subpackages call
// this in init() (e.g. internal/trackers/github/adapter.go's init
// registers NewAdapter as the github_issues factory). Re-registering
// the same kind overwrites — callers should not race.
func Register(kind TrackerKind, f factoryFunc) {
	factories[kind] = f
}

// NewByKind returns a fresh adapter for the given kind, or an error
// if no factory is registered. The ingestion driver (E2-6) calls
// this with binding.Kind to dispatch.
func NewByKind(kind TrackerKind) (TrackerAdapter, error) {
	f, ok := factories[kind]
	if !ok {
		return nil, &FactoryError{Kind: kind}
	}
	return f(), nil
}
