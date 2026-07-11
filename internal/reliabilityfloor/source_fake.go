package reliabilityfloor

import "context"

// FakeSource is a deterministic SignalSource for tests (no network).
type FakeSource struct {
	Signals []Signal
	Err     error
}

func (f *FakeSource) Fetch(ctx context.Context, projectID, query string) ([]Signal, error) {
	return f.Signals, f.Err
}
