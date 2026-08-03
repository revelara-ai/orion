package conductor

import (
	"encoding/json"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// decodeGoalsDoc parses propose_goals input, accepting non_goals as either
// structured {statement, rationale} objects (the schema, or-kjcm.6) or bare
// strings (the legacy form a model may still emit) — a mixed list works too.
func decodeGoalsDoc(in json.RawMessage) (orchestrator.GoalsDoc, error) {
	var p struct {
		Goals           []string          `json:"goals"`
		Destination     string            `json:"destination"`
		NonGoals        []json.RawMessage `json:"non_goals"`
		SuccessCriteria []string          `json:"success_criteria"`
	}
	if err := json.Unmarshal(in, &p); err != nil {
		return orchestrator.GoalsDoc{}, err
	}
	doc := orchestrator.GoalsDoc{Goals: p.Goals, Destination: strings.TrimSpace(p.Destination), SuccessCriteria: p.SuccessCriteria}
	for _, raw := range p.NonGoals {
		var ng orchestrator.NonGoal
		if err := json.Unmarshal(raw, &ng); err == nil && strings.TrimSpace(ng.Statement) != "" {
			doc.NonGoalDetails = append(doc.NonGoalDetails, ng)
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && strings.TrimSpace(s) != "" {
			doc.NonGoalDetails = append(doc.NonGoalDetails, orchestrator.NonGoal{Statement: s})
		}
	}
	return doc, nil
}
