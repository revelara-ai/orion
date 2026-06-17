# Archive — Historical Orion Artifacts

These documents are preserved for historical context. **They no longer describe Orion's current direction** and are superseded by:

- [`docs/MANIFESTO.md`](../MANIFESTO.md) — the current vision and beliefs.
- [`docs/PRD/orion-v2.md`](../PRD/orion-v2.md) — the current product requirements (Orion V2).

## Why these were archived

They belong to the **"reliability-debt remediation" lineage (Orion V1)**: Orion as a SaaS service that takes a codebase via a GitHub App, patches a narrow set of reliability gaps (timeout / retry / idempotency in Go), and opens a human-merged PR — explicitly *no auto-merge, no autonomy*.

The V2 manifesto reframes Orion as a **local-first, TUI-driven, intent-to-proven-software agentic harness** with earned autonomous delivery. That conflicts with the V1 framing on the front door (intent vs. codebase-in), the delivery bar (earned autonomy vs. always-human-merge), the runtime (local TUI vs. SaaS), and scope (any software vs. three Go patterns). Rather than edit these in place, they are kept whole as artifacts of the V1 thinking.

## Contents

- `PRD/orion-v1.md` — Orion V1 PRD (Autonomous Reliability Synthesis).
- `SPEC/Orion-SPEC.md` + `draft1..3` — the V1 specification and its adversarial-review drafts.

## Not archived (still active)

The **"Orion Triad" component specs** under `docs/PRD/` (A2A Protocol, Lookout Agent, Orchestrator Logic, Task Decomposer, Verification Engine) and `docs/TDS/Orchestrator-Decision-Matrix.md` remain active. They are largely *aligned* with the V2 manifesto (the "Liar pattern" = agents gaming the verifier; Lookout = empirical proof; Truth Alignment = proof-not-assertion). The V2 PRD references them as component-level design and flags where they need a vocabulary-refresh pass.
