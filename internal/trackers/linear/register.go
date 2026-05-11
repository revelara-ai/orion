package linear

import "github.com/revelara-ai/orion/internal/trackers"

// init registers this package's adapter factory with internal/trackers
// so callers (the ingestion driver in E2-6) can dispatch by kind
// without hard-coding the linear import.
//
// Consumers MUST blank-import this package to trigger the
// registration:
//
//	import _ "github.com/revelara-ai/orion/internal/trackers/linear"
//
// The blank import compiles unused-import-clean and the side-effect
// of init populates the factory registry.
func init() {
	trackers.Register(trackers.TrackerKindLinear, func() trackers.TrackerAdapter {
		return NewAdapter()
	})
}
