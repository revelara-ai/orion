package linear

import (
	"fmt"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/trackers"
)

// externalID builds the SPEC §4.2 stable identifier:
// "linear:<workspace_slug>#<TEAM-N>".
func externalID(workspace, identifier string) string {
	return fmt.Sprintf("linear:%s#%s", workspace, identifier)
}

// splitExternalID extracts the Linear identifier (e.g. "TEAM-12")
// from a SPEC-format external_id. Returns ok=false when the string
// is not a recognizable linear identifier.
func splitExternalID(externalID string) (string, bool) {
	if !strings.HasPrefix(externalID, "linear:") {
		return "", false
	}
	hash := strings.Index(externalID, "#")
	if hash < 0 || hash == len(externalID)-1 {
		return "", false
	}
	return externalID[hash+1:], true
}

// parseIssueListResponse extracts the "issues.nodes" array out of a
// Linear GraphQL response and normalizes each node.
func parseIssueListResponse(workspace string, data map[string]any) ([]trackers.NormalizedIssue, error) {
	issues, ok := data["issues"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("linear: response missing issues envelope")
	}
	nodes, _ := issues["nodes"].([]any)
	out := make([]trackers.NormalizedIssue, 0, len(nodes))
	for _, n := range nodes {
		node, ok := n.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, normalizeIssue(workspace, node))
	}
	return out, nil
}

// normalizeIssue maps a Linear issue GraphQL node into the canonical
// NormalizedIssue shape.
func normalizeIssue(workspace string, node map[string]any) trackers.NormalizedIssue {
	identifier := asString(node["identifier"])
	updatedAt, _ := time.Parse(time.RFC3339, asString(node["updatedAt"]))
	labels := extractLabelNames(node["labels"])
	stateType := ""
	if st, ok := node["state"].(map[string]any); ok {
		stateType = asString(st["type"])
	}
	return trackers.NormalizedIssue{
		ExternalID:  externalID(workspace, identifier),
		ExternalURL: asString(node["url"]),
		Title:       asString(node["title"]),
		Description: asString(node["description"]),
		State:       normalizeLinearStateType(stateType),
		Labels:      labels,
		LastUpdated: updatedAt,
	}
}

// extractLabelNames pulls the lower-cased, deduped label names out
// of the GraphQL "labels { nodes { name } }" shape.
func extractLabelNames(v any) []string {
	labels, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	nodes, _ := labels["nodes"].([]any)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		m, ok := n.(map[string]any)
		if !ok {
			continue
		}
		name := strings.ToLower(asString(m["name"]))
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// normalizeLinearStateType maps Linear's state.type values to the
// canonical NormalizedState enum.
func normalizeLinearStateType(t string) trackers.NormalizedState {
	switch t {
	case "backlog", "unstarted":
		return trackers.StateOpen
	case "started":
		return trackers.StateInProgress
	case "completed":
		return trackers.StateClosed
	case "canceled", "cancelled":
		return trackers.StateCancelled
	}
	return trackers.StateOpen
}

// asString is the shared "v.(string), defaulting to empty" helper.
func asString(v any) string {
	s, _ := v.(string)
	return s
}
