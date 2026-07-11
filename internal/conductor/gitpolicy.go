package conductor

import (
	"fmt"
	"regexp"
	"strings"
)

// gitTeachRefusal is the shared refusal for git mutations outside the proof
// pipeline. It TEACHES rather than just blocks: the observed failure (or-4gib)
// was a model committing unproven work to a temp branch because the tool
// description invited it — the policy is code now, and the error names the
// only legitimate path to a commit.
func gitTeachRefusal(what string) error {
	return fmt.Errorf("%s is not available to the conductor: commits happen ONLY through the proof pipeline "+
		"(build_change / build_service commit to a review branch after the gates hold). "+
		"Available git operations: status, log, diff, show, rev-parse, ls-files, blame — and "+
		"'merge --ff-only orion-…' to land a PROVEN review branch the developer approved", what)
}

// gitReadVerbs are the conductor git tool's review operations — read-only by
// construction. Fail-closed: anything not listed (and not the landing merge)
// is refused.
var gitReadVerbs = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true,
	"rev-parse": true, "ls-files": true, "blame": true,
}

// gitPolicy validates a git tool invocation against the fail-closed
// allowlist. The one permitted mutation is the exactly-shaped landing merge of
// a proven review branch: ["merge", "--ff-only", "orion-…"].
func gitPolicy(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("git: args is required")
	}
	verb := args[0]
	if gitReadVerbs[verb] {
		return nil
	}
	if verb == "merge" {
		if len(args) == 3 && args[1] == "--ff-only" && strings.HasPrefix(args[2], "orion-") {
			return nil
		}
		return gitTeachRefusal("git merge (other than 'merge --ff-only orion-…')")
	}
	return gitTeachRefusal("git " + verb)
}

// bashGitMutationRe matches git mutation verbs at COMMAND position (start of
// command or after a separator), tolerating global flags like -C <dir>.
// Guidance for honest-but-goal-driven models, not a security boundary — the
// proof sandbox (or-5ym) is the hard wall.
var bashGitMutationRe = regexp.MustCompile(`(?:^|[;&|(]\s*)git\s+(?:-\S+\s+)*(?:\S+\s+)?(commit|push|merge|rebase|reset|checkout|switch|add|stash|tag|cherry-pick|clean|filter-branch|am)\b`)

// bashGitMutation refuses bash commands that mutate git state — the same
// policy as the git tool, or the allowlist is a locked door next to an open
// window.
func bashGitMutation(command string) error {
	if m := bashGitMutationRe.FindStringSubmatch(command); m != nil {
		return gitTeachRefusal("git " + m[1] + " via bash")
	}
	return nil
}
