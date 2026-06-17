#!/bin/bash
set -euo pipefail
cd /home/josebiro/go/src/github.com/revelara-ai/orion

echo "=== CLEANING DUPLICATES ==="
# Remove the duplicate RL-0 (orion-re7.4) and the old orion-syb epic
bd close orion-re7.4 --silent 2>/dev/null || true
bd delete orion-syb 2>/dev/null || true

echo "=== ADDING REMAINING SUBTASKS ==="
for t in \
  "RL-1: Orchestrator agent — dispatches subagent work across Orion modules" \
  "RL-2: Subagent dispatch engine — spawn per-issue workers with TDD cycle" \
  "RL-3: Validator hooks — manifest criteria (A2A protocol, STAMP phases, verification) applied as post-submit gates"
do
  echo "Creating: $t"
  RESULT=$(bd create "$t" --type=subtask --priority 1 --parent orion-re7 --silent 2>&1)
  echo "Created: $RESULT"
done

echo "=== VERIFICATION ==="
echo "Current epic children:"
bd children orion-re7 2>&1 | grep -E "^├|Total:" || true

echo "=== SETUP BEADS CONFIG ==="
# Ensure ORION is the primary repo for beads
cat > /home/josebiro/go/src/github.com/revelara-ai/orion/.beads/config.yaml <<'EOF'
repos:
  primary: .
EOF
echo "Beads config written."

echo "=== COMPLETE ==="
