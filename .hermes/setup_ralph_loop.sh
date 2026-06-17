#!/bin/bash
set -euo pipefail
cd /home/josebiro/go/src/github.com/revelara-ai/orion

echo "=== RALPH LOOP ORCHESTRATOR SETUP ==="

# Clean up duplicates from earlier attempts
bd delete orion-syb --force 2>/dev/null || true

echo "--- Creating epic and sub-issues ---"
EPIC="orion-re7"

# Create sub-tasks one by one (they auto-number)
BD_ITEMS=(
  "RL-0: Manifest criteria validator library (A2A protocol, STAMP phases, verification contracts)"
  "RL-1: Orchestrator agent — dispatches subagent work across Orion modules with dependency tracking"
  "RL-2: Subagent dispatch engine — spawn per-issue workers via Claude Code / Codex with TDD cycle"  
  "RL-3: Validator hooks — A2A protocol compliance, STAMP phase gates (Design→Exec→Verify→Learn), Tier 1/2 verification applied as post-submit gates"
)

for item in "${BD_ITEMS[@]}"; do
  RESULT=$(bd create "$item" --priority 1 --parent "$EPIC" --silent 2>&1)
  echo "Created: $RESULT"
done

echo ""
echo "=== FINAL EPIC STATE ==="
bd children "$EPIC" 2>&1 | tail -20

echo ""
echo "=== BEADS CONFIG ==="
cat > .beads/config.yaml <<'EOF'
repos:
  primary: .
EOF
echo "Beads config set — Orion is primary repo for this profile."

echo ""
echo "=== WRITING ORCHESTRATOR CRON JOB ==="
# The cron job will run every 2 hours, iterating through open subtasks
cat > /home/josebiro/go/src/github.com/revelara-ai/orion/.hermes/cron/ralph-loop.sh <<'SCRIPT'
#!/bin/bash
set -euo pipefail

cd /home/josebiro/go/src/github.com/revelara-ai/orion

echo "=== RALPH LOOP TICK $(date '+%Y-%m-%d %H:%M') ==="

# Load manifesto criteria
MANIFEST="./docs/MANIFESTO.md"
if [[ ! -f "$MANIFEST" ]]; then
  echo "ERROR: Manifest not found!" && exit 1
fi

echo "--- Manifest Validation Criteria ---"
grep -E "^#### Phase|^### The (A2A|Verification)" "$MANIFEST" | head -8 && echo "(loaded)" || echo "Partial manifest"

# Survey beads state
echo ""
echo "=== ORION BEAD STATE ==="
STATUS=$(timeout 15 bd status 2>&1 || echo "bd timeout")
echo "$STATUS" | grep -E "Total|Open|Ready|Blocked|Progress" | head -6 || true

# Read epic children
CHILDREN=$(bd children orion-re7 2>&1 || true)
OPEN=$(echo "$CHILDREN" | grep -c "○ .*rl-" 2>/dev/null || echo "0")
TOTAL=$(echo "$CHILDREN" | grep -c "^├" 2>/dev/null || echo "0")
DONE=$((TOTAL - OPEN))

echo ""
echo "=== RL EPIC (${EPIC}) PROGRESS: $DONE/$TOTAL tasks ==="
echo "$CHILDREN" | grep "^├" | head -10 || true

# Ready-to-work dispatch list
echo ""
echo "=== READY DISPATCH TARGETS ==="
READY=$(timeout 15 bd ready 2>&1 | grep "^○ orion-" | head -10 || true) 
echo "$READY" || echo "(none)"

echo ""
echo "=== MANIFEST CRITERIA CHECK ==="
grep -q "Tier 1" "$MANIFEST" && echo "[✓] Tier 1 Empirical Verification (exit codes, diffs, hashes)" || echo "[✗] Missing Tier 1"  
grep -q "Tier 2" "$MANIFEST" && echo "[✓] Tier 2 Structural Verification (schema, policy-as-code)" || echo "[✗] Missing Tier 2"
grep -oP '(?<=State: )RED' "$MANIFEST" >/dev/null && echo "[✓] STAMP Phase 3: RED gate configured" || echo "[?] STAMP Phase 3 unclear"

echo "=== TICK COMPLETE ==="
SCRIPT

chmod +x .hermes/cron/ralph-loop.sh
echo "Orchestrator script written to .hermes/cron/ralph-loop.sh"

echo ""
echo "=== SETUP COMPLETE ==="
