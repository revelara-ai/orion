package agent

import (
	"context"
	"sync"
)

// InMemoryScopeRecorder is a test-only ScopeRecorder that stores
// records in memory. The production wiring (cmd/orion-worker) binds
// a recorder that writes to repos.ScopeRequestRepo.
type InMemoryScopeRecorder struct {
	mu      sync.Mutex
	records []ScopeRecord
}

// NewInMemoryScopeRecorder returns an empty recorder.
func NewInMemoryScopeRecorder() *InMemoryScopeRecorder {
	return &InMemoryScopeRecorder{}
}

// Record stores the record in memory.
func (r *InMemoryScopeRecorder) Record(_ context.Context, in ScopeRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, in)
	return nil
}

// Records returns a snapshot of stored records.
func (r *InMemoryScopeRecorder) Records() []ScopeRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ScopeRecord, len(r.records))
	copy(out, r.records)
	return out
}

// CountByTool returns the number of records for the named tool.
func (r *InMemoryScopeRecorder) CountByTool(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, rec := range r.records {
		if rec.ToolName == name {
			n++
		}
	}
	return n
}

// CountRejections returns the number of records that carry a
// rejection reason.
func (r *InMemoryScopeRecorder) CountRejections() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, rec := range r.records {
		if rec.RejectionReason != nil {
			n++
		}
	}
	return n
}

// noopEventSink is the default EventSink for production code paths
// that don't have a more interesting sink. The Lookout in orion-e47
// wires the real sink that updates last_event_at on the worker_session.
type noopEventSink struct{}

// NewNoopEventSink returns a sink that discards events. Useful for
// unit tests of code paths that don't care about events.
func NewNoopEventSink() EventSink { return noopEventSink{} }

// Emit drops the event.
func (noopEventSink) Emit(_ context.Context, _ Event) error { return nil }

// recordingEventSink keeps every emitted Event in memory; used by
// the runner unit tests.
type recordingEventSink struct {
	mu     sync.Mutex
	events []Event
}

// NewRecordingEventSink returns an EventSink that captures all
// emitted Events for inspection.
func NewRecordingEventSink() *recordingEventSink { return &recordingEventSink{} }

// Emit appends the event.
func (s *recordingEventSink) Emit(_ context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

// Events returns a snapshot.
func (s *recordingEventSink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}
