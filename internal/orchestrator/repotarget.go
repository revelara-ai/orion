package orchestrator

import "strings"

// repoTargetFromLayout interprets the ratified direction.repo_layout decision
// (or-045a.7): a PATH-shaped answer ("~/src/my-game", "/abs/path", "./rel")
// is the project's own repo target; the "managed-repo" default (or any
// non-path phrase) keeps the managed repo. Deterministic: only an answer that
// looks like a filesystem path selects a target.
func repoTargetFromLayout(layout string) string {
	v := strings.TrimSpace(layout)
	if v == "" || strings.EqualFold(v, "managed-repo") {
		return ""
	}
	if strings.ContainsAny(v, " \t") {
		return "" // a phrase ("new standalone repo"), not a path — the agent must elicit the path
	}
	if strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~/") || strings.HasPrefix(v, "./") || strings.HasPrefix(v, "../") {
		return v
	}
	return ""
}
