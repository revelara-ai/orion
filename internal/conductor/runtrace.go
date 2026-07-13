package conductor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// First-class run observability (or-kzf.3): the persisted run_events
// (or-v9f.16) ARE the trace — phases, per-task attribution, verdicts,
// timings. This layer makes them QUERYABLE (a per-run summary computed from
// the trace) and adds the DRIFT signal across runs: rising refinement
// attempts per task, or a falling first-pass proof rate, is the "quietly
// getting worse" the paper warns about.

// RunTraceSummary is one run's derived metrics.
type RunTraceSummary struct {
	RunID            string
	Events           int
	Tasks            int     // tasks that emitted any attributed event
	Attempts         int     // Generate phase starts across all tasks
	FirstPassAccepts int     // tasks whose FIRST Prove outcome was Accept
	ProvenTasks      int     // tasks with any Accept
	Failed           bool    // the run closed failed (or never closed)
	WallSeconds      float64 // first event → last event
}

// AttemptsPerTask is the refinement-pressure metric.
func (s RunTraceSummary) AttemptsPerTask() float64 {
	if s.Tasks == 0 {
		return 0
	}
	return float64(s.Attempts) / float64(s.Tasks)
}

// FirstPassRate is the fraction of tasks proven on their first attempt.
func (s RunTraceSummary) FirstPassRate() float64 {
	if s.Tasks == 0 {
		return 0
	}
	return float64(s.FirstPassAccepts) / float64(s.Tasks)
}

// SummarizeRun derives a run's metrics from its persisted trace.
func SummarizeRun(ctx context.Context, store *contextstore.Store, runID string) (RunTraceSummary, error) {
	events, err := store.ListRunEventsAfter(ctx, runID, 0)
	if err != nil {
		return RunTraceSummary{}, err
	}
	if len(events) == 0 {
		return RunTraceSummary{}, fmt.Errorf("no trace recorded for run %s", runID)
	}
	sum := RunTraceSummary{RunID: runID, Events: len(events), Failed: true}
	tasks := map[string]bool{}
	firstProve := map[string]string{} // task → first Prove outcome detail
	accepted := map[string]bool{}
	for _, e := range events {
		if e.TaskID != "" {
			tasks[e.TaskID] = true
		}
		switch {
		case e.TaskID != "" && e.Phase == "Generate" && e.Status == "running":
			sum.Attempts++
		case e.TaskID != "" && e.Phase == "Prove" && (e.Status == "done" || e.Status == "warn"):
			if _, seen := firstProve[e.TaskID]; !seen {
				firstProve[e.TaskID] = e.Detail
			}
			if strings.HasPrefix(e.Detail, "Accept") || strings.HasPrefix(e.Detail, "reused") {
				accepted[e.TaskID] = true
			}
		case e.Phase == "Run" && e.Status == "done":
			sum.Failed = false
		}
	}
	sum.Tasks = len(tasks)
	for task, detail := range firstProve {
		_ = task
		if detail == "Accept" || strings.HasPrefix(detail, "reused") {
			sum.FirstPassAccepts++
		}
	}
	sum.ProvenTasks = len(accepted)
	first, ferr := time.Parse(time.RFC3339Nano, events[0].CreatedAt)
	last, lerr := time.Parse(time.RFC3339Nano, events[len(events)-1].CreatedAt)
	if ferr == nil && lerr == nil {
		sum.WallSeconds = last.Sub(first).Seconds()
	}
	return sum, nil
}

// DriftSignal compares the current run to the previous one and names any
// degradation: rising attempts per task, or a falling first-pass proof rate.
// Empty string = no drift indicated.
func DriftSignal(prev, cur RunTraceSummary) string {
	if prev.Tasks == 0 || cur.Tasks == 0 {
		return ""
	}
	var signals []string
	if cur.AttemptsPerTask() > prev.AttemptsPerTask() {
		signals = append(signals, fmt.Sprintf("refinement pressure rising: %.2f → %.2f attempts/task", prev.AttemptsPerTask(), cur.AttemptsPerTask()))
	}
	if cur.FirstPassRate() < prev.FirstPassRate() {
		signals = append(signals, fmt.Sprintf("first-pass proof rate falling: %.0f%% → %.0f%%", prev.FirstPassRate()*100, cur.FirstPassRate()*100))
	}
	if len(signals) == 0 {
		return ""
	}
	return "DRIFT: " + strings.Join(signals, "; ")
}
