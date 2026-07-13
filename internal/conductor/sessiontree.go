package conductor

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/revelara-ai/orion/pkg/llm"
)

// Tree-structured, forkable sessions (or-ykz.5, Pi parity): a session can be
// forked at any prior developer turn into a NEW branch sharing that ancestry —
// non-destructive by construction (the source's messages are copied, never
// moved), so exploring an alternate spec/decomposition/approach never rebuilds
// from scratch and never disturbs the original line of thought.
//
// The "SESSION:<id> · <text>" result sentinel mirrors /model's MODEL: — the
// TUI switches its active ACP session to the returned id.

// sessionNode is one branch's fork metadata (roots have no entry).
type sessionNode struct {
	Parent   string
	ForkTurn int
}

// fork creates a new branch from sessionID's history through developer turn
// arg (1-based). Empty arg forks at the head (= clone semantics).
func (a *OrionAgent) fork(sessionID, arg string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	src := a.sessions[sessionID]
	total := turnsIn(src)
	if total == 0 {
		return "Nothing to fork — this session has no turns yet.", nil
	}
	turn := total
	if s := strings.TrimSpace(arg); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Sprintf("fork: %q is not a turn number (1..%d)", s, total), nil
		}
		if n < 1 || n > total {
			return fmt.Sprintf("fork: turn %d out of range (this session has %d turns)", n, total), nil
		}
		turn = n
	}
	prefix := messagesThroughTurn(src, turn)
	branch := append([]llm.Message(nil), prefix...) // copy: divergence never touches the source
	id := a.newBranchIDLocked(sessionID)
	a.sessions[id] = branch
	if a.tree == nil {
		a.tree = map[string]sessionNode{}
	}
	a.tree[id] = sessionNode{Parent: sessionID, ForkTurn: turn}
	return fmt.Sprintf("SESSION:%s · forked %s at turn %d/%d (%d messages) — you are now on the new branch; /tree to navigate, /switch %s to go back",
		id, sessionID, turn, total, len(branch), sessionID), nil
}

// cloneSession is fork-at-head: a full private copy of the conversation.
func (a *OrionAgent) cloneSession(sessionID string) (string, error) {
	return a.fork(sessionID, "")
}

// switchSession validates the target branch and returns the switch sentinel.
func (a *OrionAgent) switchSession(_ string, arg string) (string, error) {
	target := strings.TrimSpace(arg)
	a.mu.Lock()
	_, ok := a.sessions[target]
	turns := turnsIn(a.sessions[target])
	a.mu.Unlock()
	if target == "" || !ok {
		return fmt.Sprintf("switch: unknown session %q — /tree lists the branches", target), nil
	}
	return fmt.Sprintf("SESSION:%s · switched (%d turns)", target, turns), nil
}

// treeView renders the ancestry tree around sessionID: walk to the root, then
// depth-first over every branch, marking the current one.
func (a *OrionAgent) treeView(sessionID string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	root := sessionID
	for {
		n, ok := a.tree[root]
		if !ok || n.Parent == "" {
			break
		}
		root = n.Parent
	}
	children := map[string][]string{}
	for id, n := range a.tree {
		children[n.Parent] = append(children[n.Parent], id)
	}
	for _, c := range children {
		sort.Strings(c)
	}
	var b strings.Builder
	b.WriteString("session tree (fork with /fork [turn], jump with /switch <id>):\n")
	var render func(id string, depth int)
	render = func(id string, depth int) {
		marker := ""
		if id == sessionID {
			marker = "   ← you are here"
		}
		at := ""
		if n, ok := a.tree[id]; ok {
			at = fmt.Sprintf(" (forked at turn %d)", n.ForkTurn)
		}
		fmt.Fprintf(&b, "%s%s — %d turns%s%s\n", strings.Repeat("  ", depth), id, turnsIn(a.sessions[id]), at, marker)
		for _, c := range children[id] {
			render(c, depth+1)
		}
	}
	render(root, 0)
	return b.String(), nil
}

// newBranchIDLocked mints a collision-free branch id derived from the source.
func (a *OrionAgent) newBranchIDLocked(src string) string {
	base := src
	// Keep ids short: fork-of-fork appends to the ROOT name, not the whole chain.
	if i := strings.Index(base, "-f"); i > 0 {
		base = base[:i]
	}
	for n := 1; ; n++ {
		id := fmt.Sprintf("%s-f%d", base, n)
		if _, exists := a.sessions[id]; !exists {
			return id
		}
	}
}

// turnsIn counts developer turns (user TEXT messages — tool results are
// user-role but carry no text).
func turnsIn(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		if isDeveloperTurn(m) {
			n++
		}
	}
	return n
}

// messagesThroughTurn returns the prefix through the END of developer turn n:
// everything up to (excluding) the (n+1)th developer text message.
func messagesThroughTurn(msgs []llm.Message, n int) []llm.Message {
	seen := 0
	for i, m := range msgs {
		if isDeveloperTurn(m) {
			seen++
			if seen == n+1 {
				return msgs[:i]
			}
		}
	}
	return msgs
}

func isDeveloperTurn(m llm.Message) bool {
	if m.Role != llm.RoleUser {
		return false
	}
	for _, c := range m.Content {
		if c.Type == llm.BlockText && strings.TrimSpace(c.Text) != "" {
			return true
		}
	}
	return false
}
