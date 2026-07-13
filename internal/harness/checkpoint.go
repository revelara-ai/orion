package harness

import (
	"context"

	"github.com/revelara-ai/orion/pkg/llm"
)

// Provider-outage turn checkpoint (or-mvr.8, C11 / the inc-qdi 529 class): a
// provider-wide outage mid-turn used to discard the half-generated
// conversation — every tool call already executed, every file already
// written, re-derived from scratch on the next attempt. With a checkpoint
// store wired, a provider-class failure PERSISTS the conversation before the
// error surfaces; the next Run under the same key resumes from it (the files
// its tool calls wrote are still on disk), and a successful turn clears it.
// Only ErrProvider saves: a stall, cap, or budget stop is the turn's own
// verdict, not a dead dependency, and must not replay.

// TurnCheckpoint persists a half-generated turn across process/provider death.
type TurnCheckpoint interface {
	Load(ctx context.Context, key string) ([]llm.Message, bool)
	Save(ctx context.Context, key string, convo []llm.Message)
	Clear(ctx context.Context, key string)
}

// resumeFromCheckpoint swaps the kickoff conversation for a persisted one, if
// any. Returns the conversation to run and whether a resume happened.
func (l *Loop) resumeFromCheckpoint(ctx context.Context, start []llm.Message) ([]llm.Message, bool) {
	if l.Checkpoint == nil || l.CheckpointKey == "" {
		return start, false
	}
	saved, ok := l.Checkpoint.Load(ctx, l.CheckpointKey)
	if !ok || len(saved) == 0 {
		return start, false
	}
	return saved, true
}

// checkpointOnProviderFailure persists the conversation on a provider-class
// turn death. Best-effort by contract (implementations swallow their errors).
func (l *Loop) checkpointOnProviderFailure(ctx context.Context, convo []llm.Message) {
	if l.Checkpoint != nil && l.CheckpointKey != "" {
		l.Checkpoint.Save(ctx, l.CheckpointKey, convo)
	}
}

// clearCheckpoint drops the checkpoint after a successfully completed turn.
func (l *Loop) clearCheckpoint(ctx context.Context) {
	if l.Checkpoint != nil && l.CheckpointKey != "" {
		l.Checkpoint.Clear(ctx, l.CheckpointKey)
	}
}
