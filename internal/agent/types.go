// Package agent implements the in-worker LLM interaction substrate
// (SPEC §11.2-§11.4). The AgentRunner mediates every LLM call; tools
// are explicitly enumerated with structural enforcement; every tool
// dispatch records a ScopeRequest audit row.
package agent

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// SessionID identifies one agent session. Sessions are opaque to the
// caller; the runner owns them.
type SessionID string

// Prompt is the system prompt that scopes a session. Per SPEC §11.4
// continuation turns are terse; the prompt is the load-bearing scoping
// surface.
type Prompt struct {
	System          string
	Model           string
	TokenBudget     int
	SnapshotContext map[string]any
}

// ToolDef declares one tool the agent may call during a Turn. The
// agent receives this enumeration along with each Turn; tools outside
// the enumeration cannot be invoked.
type ToolDef struct {
	Name        string
	Description string
	JSONSchema  map[string]any
}

// ToolCall is one tool invocation requested by the agent during a Turn.
type ToolCall struct {
	Name string
	Args map[string]any
}

// ToolResult is the structured outcome of a ToolCall. Status describes
// whether the call was admitted, rejected, or errored.
type ToolResult struct {
	Status       ToolStatus
	Result       map[string]any
	RejectReason string
}

// ToolStatus enumerates the three outcomes the ScopeRequest ledger
// distinguishes.
type ToolStatus string

// Outcomes.
const (
	ToolAccepted ToolStatus = "accepted"
	ToolRejected ToolStatus = "rejected" // structural enforcement said no
	ToolErrored  ToolStatus = "errored"  // accepted, then runtime error
)

// TurnResult is the structured outcome of a Turn.
type TurnResult struct {
	SessionID    SessionID
	FinishReason FinishReason
	TokensIn     int
	TokensOut    int
	ToolCalls    []ToolCall
	ToolResults  []ToolResult
	Response     string
}

// FinishReason enumerates the §11.4 termination conditions.
type FinishReason string

// Reasons.
const (
	FinishStop            FinishReason = "stop"             // normal stop
	FinishToolUse         FinishReason = "tool_use"         // model emitted tool calls
	FinishMaxTokens       FinishReason = "max_tokens"       // hit per-turn limit
	FinishBudgetExhausted FinishReason = "budget_exhausted" // hit per-session token budget (§11.4 #3)
	FinishCancelled       FinishReason = "cancelled"        // caller cancelled
	FinishError           FinishReason = "error"            // transport / parse error
)

// EventKind is the discriminator for incremental events emitted during
// a Turn (SPEC §11.2). last_event_at on the WorkerSession row is
// updated as each event lands; that's the Lookout's heartbeat.
type EventKind string

// Kinds.
const (
	EventTokensInProgress  EventKind = "tokens_in_progress" //nolint:gosec // G101: event kind literal, not a credential
	EventToolCallRequested EventKind = "tool_call_requested"
	EventToolResult        EventKind = "tool_result"
	EventTurnComplete      EventKind = "turn_complete"
)

// Event is one incremental Turn event.
type Event struct {
	Kind       EventKind
	SessionID  SessionID
	OccurredAt time.Time
	// Payload is the structured contents specific to Kind. The
	// JSON-serialized form is what gets persisted by the Lookout when
	// it records heartbeats.
	Payload map[string]any
}

// EventSink consumes events as they're emitted. Implementations MUST
// be safe to call from the Turn goroutine. The WorkerSession heartbeat
// recorder + the Lookout's tail-log consumer both bind a sink here.
type EventSink interface {
	Emit(ctx context.Context, e Event) error
}

// ScopeRecorder persists ScopeRequest audit rows. Decoupled from the
// concrete *repos.ScopeRequestRepo so the agent package has no upward
// dep on repos; the cmd/orion-worker binary wires the concrete impl.
type ScopeRecorder interface {
	Record(ctx context.Context, in ScopeRecord) error
}

// ScopeRecord mirrors repos.ScopeRequestInput in the agent's vocab.
// The fields are intentionally identical so the worker can pass it
// through.
type ScopeRecord struct {
	RunID           uuid.UUID
	ClaimID         *uuid.UUID
	WorkerSessionID *uuid.UUID
	ToolName        string
	RequestedScope  map[string]any
	GrantedScope    map[string]any
	RejectionReason *string
}

// ErrSessionNotFound is returned by Turn/Cancel for an unknown sid.
var ErrSessionNotFound = errors.New("agent: session not found")

// ErrTokenBudgetExhausted is returned when a Turn would exceed the
// session's TokenBudget per SPEC §11.4 #3.
var ErrTokenBudgetExhausted = errors.New("agent: token budget exhausted")
