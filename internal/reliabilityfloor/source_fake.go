package reliabilityfloor

import "context"

// FakeSource is a deterministic SignalSource for tests (no network).
type FakeSource struct {
	Signals []Signal
	Err     error
}

// Fetch returns the canned signals (test double).
func (f *FakeSource) Fetch(_ context.Context, _, _ string) ([]Signal, error) {
	return f.Signals, f.Err
}
