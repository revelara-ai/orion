#!/bin/bash
set -euo pipefail
cd /home/josebiro/go/src/github.com/revelara-ai/orion

echo "=== RALPH LOOP TICK $(date '+%Y-%m-%d %H:%M') ==="

# 1. Read manifesto criteria
MANIFESTO="./docs/MANIFESTO.md"
if [[ ! -f "$MANIFESTO" ]]; then
  echo "ERROR: Manifesto not found at $MANIFESTO"
  exit 1
fi

echo "--- Manifesto phases ---"
grep -E "^#### Phase|^### The (Orphan|Verification|A2A)" "$MANIFESTO" || true

# 2. Survey current state
echo ""
echo "=== ORION BEAD STATE ==="
STATUS=$(timeout 15 bd status 2>&1 || echo "bd timeout")
echo "$STATUS" | grep -E "Total|Open|Ready|Blocked|In Progress" || echo "No data from bd status"

# 3. Read epic children
echo ""
echo "=== RALPH LOOP EPIC (orion-re7) CHILDREN ==="
CHILDREN=$(bd children orion-re7 2>&1 | grep -E "^├")
echo "$CHILDREN" || echo "(no children)"

# 4. Check progress
OPEN_COUNT=$(echo "$CHILDREN" | grep -c "○" 2>/dev/null || echo "0")
TOTAL_COUNT=$(echo "$CHILDREN" | grep -c "^├" 2>/dev/null || echo "0")
echo "Progress: $((TOTAL_COUNT - OPEN_COUNT))/$TOTAL_COUNT tasks complete"

# 5. Show ready issues that could be picked up by the loop
echo ""
echo "=== READY TO WORK (for dispatch) ==="
BD_LIST=$(timeout 15 bd ready 2>&1 || echo "bd timeout")
echo "$BD_LIST" | grep -E "^○|Total:" || echo "No ready items"

# 6. Manifest validation checklist against Orion's STAMP criteria
echo ""
echo "=== MANIFEST CRITERIA CHECK ==="
echo "$MANIFESTO" | grep -oP '(?<=\*\*State: )RED \\\\$' >/dev/null 2>&1 && echo "STAMP Phase 3 (RED gate): configured ✓" || echo "STAMP Phase 3 (RED gate): check needed"
grep -q "A2A" "$MANIFESTO" && echo "A2A Protocol defined: ✓" || echo "A2A Protocol: ✗"
grep -q "Tier 1" "$MANIFESTO" && echo "Verification Taxonomy Tier 1: ✓" || echo "Verify Taxonomy T1: ✗"
grep -q "Tier 2" "$MANIFESTO" && echo "Verification Taxonomy Tier 2: ✓" || echo "Verification Taxonomy T2: ✗"

echo ""
echo "=== RALPH LOOP COMPLETE ==="
