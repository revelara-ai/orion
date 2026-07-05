---
title: Harness Feature Comparison — OpenClaw, Pi, Hermes, Claude Flow V3 → Orion
status: research
author: Joseph Bironas (with agent research)
created: 2026-06-21
updated: 2026-06-30
sources:
  - local install OpenClaw (CLI 2026.4.10 / npm openclaw 2026.5.28 / clawhub 0.4.0)
  - local install Pi (@earendil-works/pi-coding-agent 0.79.8, pi.dev)
  - local install Hermes (0.16.0 / build 2026.6.5, Nous Research)
  - local clone Claude Flow V3 (ruvnet/claude-flow, 3.0.0-alpha / cli 3.14.2; commit 8ae8752; ~17 @claude-flow packages + 15 plugins) — read from source, cross-checked against an understand-anything knowledge graph of the CLI
  - docs/PRD/orion-v2.md
related_beads_epic: <set after filing — cross-harness feature parity>
---

# Harness Feature Comparison: OpenClaw · Pi · Hermes · Claude Flow V3 → Orion

> Goal: inventory the features/capabilities of peer agentic harnesses, then identify
> the gaps worth closing in Orion. Each harness was researched from its **local installation**
> (binaries + full source/skills), not from marketing. Orion status is judged against
> `docs/PRD/orion-v2.md`. The "necessary additions" are filed as beads issues (see end).
>
> **2026-06-30 addendum — Claude Flow V3 (§4):** added as the fourth peer because it is the
> *first* harness here that claims Orion's own territory by name — a `@claude-flow/guidance`
> "control plane" with **compile / enforce / prove / evolve**, **gates**, **proof**, **trust**,
> **adversarial**, **truth-anchors**, and a **ledger**. Read in source, the vocabulary collides
> with Orion's but the mechanism does not: it governs the *process* (did the agent follow the
> rules) with tamper-evident audit, and never proves the *result* (is the code correct). The
> nearest-vocabulary competitor turns out to *sharpen* Orion's wedge rather than threaten it.

## TL;DR — what each harness *is*

| Harness | One-liner | Primary surface | Distinctive bet |
|---|---|---|---|
| **OpenClaw** (2026.5.28, MIT, `openclaw/openclaw`) | Personal AI-assistant **gateway**; coding is one skill among many | 20+ chat channels + TUI + dashboard + companion nodes | Always-on multi-channel gateway, persona+self-curated memory ("SOUL"), 46-provider failover, ClawHub registry, Mantis live-transport verification |
| **Pi** (0.79.8, MIT, pi.dev, Mario Zechner) | Minimal **terminal coding harness**, "primitives over features" | Chat-first TUI + print/json/rpc + embeddable SDK | 4 default tools + everything-else-as-editable-extension, npm/git packages, `/reload` hot-reload, ~30-hook extension bus, tree-structured forkable sessions |
| **Hermes** (0.16.0, MIT, Nous Research; lineage: OpenClaw fork) | **Self-improving** autonomous agent that "runs anywhere" | Multi-platform gateway + TUI + ACP + dashboard | Closed self-improvement loop (writes its own skills/memory), Kanban swarm, run-anywhere backends w/ serverless hibernation, aggressive supply-chain hardening, gamified autonomy |
| **Claude Flow V3** (3.0.0-alpha / cli 3.14.2, MIT, ruvnet) | **Swarm-orchestration framework + governance "control plane"** for Claude Code sessions | Headless CLI + dual MCP server (no TUI) | "15-agent hierarchical-mesh / Queen" swarm; a `guidance` control plane (compile/enforce/prove/evolve) with deterministic gates + HMAC audit ledger; SONA/neural learning; AgentDB/HNSW memory; metaharness (scores/mints other harnesses). **Headline perf + autonomy claims are self-refuted by the repo's own honesty tables.** |

**Where Orion sits:** Orion V2 is a **reliability-first, proof-gated, local-first control plane** that
*drives the developer's own coding agent over ACP* — it is not an LLM client. Its differentiator is the
independent multi-modal proof harness (behavioral + empirical + hazard), trust-domain separation
("no agent grades its own homework"), and the Polaris reliability loop. OpenClaw, Pi, and Hermes are mostly
**model-clients with rich UX/extensibility/multi-channel reach**; Orion already exceeds them on
*verification rigor and coordination discipline*, but trails on *extensibility ergonomics, session UX,
fast-feedback signals, self-improvement mechanics, and harness self-operability*. Those trailing areas are
the gap list.

**Claude Flow V3 is the exception that proves the rule.** It is the only peer that adopts Orion's exact
vocabulary — a "control plane" that *proves* and *gates* and accrues *trust* — so it is the one direct test
of whether Orion's differentiation survives contact with a competitor aiming at the same target. Read in
source, it does: Claude Flow's "proof" is an HMAC-signed audit record of **what the agent says it did**, not
independent verification that the code is correct; its "trust" is a score an agent **earns by passing its own
regex gates** (then spends as 2× autonomy) — the precise self-certification Orion was built to forbid; and the
swarm underneath is largely **simulated** (agents are objects in a `Map`, every model-call path returns a
placeholder string, with no LLM SDK anywhere in the tree). Orion *gains* separation from Claude Flow on its
core axis (independent, multi-modal, author-withheld correctness proof), while genuinely **trailing** it on a
short, real list: a production-grade MCP server (OAuth 2.1 + server-initiated sampling), second-order
prompt-injection filtering of tool/MCP output, and event-sourced claim-based coordination.

---

## 1. OpenClaw — capability inventory

**What it is:** a long-lived local **Gateway** (WebSocket control plane) routing inbound messages from 20+
chat channels to isolated, persona-bearing agents. "The Gateway is the control plane — the product is the assistant."

- **CLI surface (~40 command groups):** `gateway`, `agent(s)`, `tui`, `dashboard`, `sessions`, `tasks` (+ TaskFlow), `acp`, `channels`, `message` (rich: polls/reactions/threads/roles/bans), `models`/`infer`/`capability`, `skills`, `plugins`, `hooks`, `mcp` (client **and** `mcp serve`), `config`/`configure`/`secrets`, `security audit`, `approvals`/`exec-policy` (yolo|cautious|deny-all), `sandbox`, `cron`, `doctor --fix`, `backup`, `nodes`, `pairing`/`devices`/`qr`.
- **Orchestration:** isolated per-agent "brains" (own workspace/SOUL/auth/sessions); routing **bindings** (channel/peer/guild→agent); **sub-agent spawning** (`sessions_spawn`, isolated, non-blocking, cheaper model); cross-session messaging; **parallel specialist lanes** (parallelism as a scarce-resource problem); **command queue + steering**; **delegate architecture** (named org agent acting "on behalf of" under standing orders); **ACP bridge** to drive Claude Code/Codex/Gemini/Cursor/OpenCode as background workers.
- **Skills/packages:** AgentSkills `SKILL.md` format; progressive on-demand loading (compact XML catalog injected, refs loaded when needed); hot-reload via file watcher; 3-tier precedence; ~57 bundled skills; **ClawHub** public registry (`publish/install/sync`, server-side security scan + review gate); **plugins** (providers/channels/tools/hooks/context-engines/memory-backends) from npm/archive/path/`clawhub:`/Claude-compatible marketplace.
- **Memory:** workspace markdown (`MEMORY.md`, daily notes), **memory-core** plugin (semantic `memory_search`), pluggable backends (QMD/LanceDB/Honcho/wiki), **Dreaming** (background consolidation Light/Deep/REM → `DREAMS.md`), **Commitments** (inferred follow-ups), Active Memory pre-search, pluggable **context engine** + compaction.
- **Tools/MCP:** `exec`/`process`/`code_execution`/`apply_patch`/`read/write/edit`, `web_search` (11 backends), `browser` (noVNC-sandboxable), `tool_search`/`tool_describe` (semantic tool discovery for large catalogs), tool **profiles** (coding vs messaging), MCP **both directions**.
- **TUI/UX:** terminal TUI + web Control UI + native nodes; **Live Canvas / A2UI** (agent-driven UI); slash commands; **progress drafts** (one self-updating WIP message); streaming modes; approval prompts.
- **Sandbox/safety:** modes off|non-main|all; backends Docker/SSH/OpenShell; `exec-policy` presets; DM pairing for untrusted senders; `security audit --deep --fix`; FS-safe atomic writes; npm-shrinkwrap. **Explicitly single-operator, not hostile multi-tenant.**
- **Verification:** **Mantis** — live-transport before/after bug reproduction with deterministic oracles + screenshots + PR artifacts; **QA lab** scenario packs; `doctor --fix`; coding-agent skill's review-until-clean loop.
- **Models:** radically agnostic — **46 provider plugins**, auth-profile rotation + multi-stage failover + per-capability routing (text/image/pdf/tts/video/embedding), thinking levels off→max.
- **Distinctive:** gateway-as-control-plane; persona continuity (SOUL/IDENTITY/self-defining bootstrap); heartbeat proactivity; companion-node hardware fabric; ClawHub; Mantis; MCP-server + ACP federation of other harnesses.

## 2. Pi — capability inventory

**What it is:** `@earendil-works/pi-coding-agent` — a minimal terminal coding harness. Philosophy:
*"powerful defaults but skips features"* — a small core you reshape with extensions instead of forking.
Split into `pi-ai` / `pi-agent-core` / `pi-tui` / `pi-coding-agent` (the harness is also an embeddable SDK; OpenClaw builds on it).

- **CLI:** `pi` (interactive) / `pi "<prompt>"` / `pi @file @img "..."` / `-p` print / `--mode text|json|rpc` / `--export`→HTML; package subcommands `install/remove/update/list/config`; rich model/session/tool/resource flags; every resource flag has a `--no-*`.
- **Core primitives:** **4 default tools** `read/write/edit/bash` + 3 opt-in read-only `grep/find/ls`. **Deliberately omits** MCP, sub-agents, plan-mode, to-dos, permission popups, background bash — each shipped instead as a ~100-line **example extension**.
- **Skills:** AgentSkills standard; progressive disclosure (name+description injected; `SKILL.md` loaded on demand or via `/skill:name`); discovery from global/project/package dirs; **can point at `~/.claude/skills`, `~/.codex/skills`** (cross-harness reuse).
- **Packages:** npm/git-distributed bundles of **extensions+skills+prompts+themes** via a `pi` key in `package.json`; global vs project (`-l`) scope; per-resource filtering; `pi.dev/packages` gallery. A real package manager on existing registries — no bespoke OCI.
- **Subagents (example extension, not core):** `subagent` tool with single/parallel(max 8, 4 concurrent, streaming)/chain modes; each subagent = isolated child `pi --mode json -p --no-session` **process**; markdown agent defs (model+tools per agent); ships `/implement` (scout→planner→worker), `/scout-and-plan`, `/implement-and-review` presets.
- **`/reload` + runtime tool registration:** hot-reload keybindings/extensions/skills/prompts/context-files **without restart**; themes auto-reload; tools registerable after startup; the agent can write & load its own tools mid-session ("ask pi to build what you want").
- **Memory/context:** `AGENTS.md`/`CLAUDE.md` auto-loaded up the tree; **compaction** (LLM-summarize older turns, keep recent, full history stays in JSONL); branch summarization. No vector/LTM in core.
- **TUI/UX:** chat-first; `@` fuzzy file refs, Tab completion, image paste, `!cmd`/`!!cmd` inline shell; ~25 slash commands (`/tree`,`/fork`,`/clone`,`/compact`,`/export`,`/share`,`/reload`…); Ctrl+P model cycling, Shift+Tab thinking level; **message-queue steering** (Enter=after turn, Alt+Enter=after all work, Esc aborts); **extensions can replace the entire UI** (vim editor, games, status line/widgets/overlays).
- **Sessions:** **tree-structured JSONL** (every entry has `id`+`parentId` → in-place branching, no file sprawl); `-c/-r/--session/--fork/--no-session`; `/tree` (jump/search/fold/bookmark), `/fork`, `/clone`, `/export`→HTML, `/share`→private gist.
- **Sandbox/safety:** **none built-in by design** — isolation must come from OS/VM/container (`sandbox`, `gondolin` micro-VM, Docker examples); **Project Trust** is the one core guard (loads inputs only after a per-dir trust decision).
- **Models:** ~35 providers (subscription OR API key), custom providers via `models.json`, per-model thinking budgets, default `google`.
- **Distinctive:** primitives-over-features; npm/git packages; `/reload` + runtime tool registration (self-modifying); ~30-hook extension bus exposing nearly the whole agent loop (tool gating, provider swap, system-prompt rewrite, custom compaction, full UI replacement); tree-forkable sessions; harness-as-library (interactive/print/json/rpc + SDK); subagents-by-subprocess.

## 3. Hermes — capability inventory

**What it is:** "the self-improving AI agent — creates skills from experience, improves them during use,
and runs anywhere." Python; by Nous Research; lineage = OpenClaw fork/rebrand (`claw migrate` tooling).

- **CLI (~55 subcommands):** `chat` (default), `model`/`fallback`/`proxy`/`auth`/`portal`/`secrets`, `gateway`/`whatsapp`/`slack`/`send`/`pairing`, `cron`/`webhook`/`kanban`, `skills`/`bundles`/`plugins`/`curator`, `tools`/`mcp` (+`serve`/`catalog`)/`computer-use`, `sessions`/`memory`/`insights`/`checkpoints`, `profile`, `doctor`/`status`/`security`/`backup`/`logs`, `dashboard`/`desktop`/`acp`/`claw`. Global `--oneshot`, `--worktree`, `--yolo`, `--safe-mode`.
- **Autonomy/orchestration:** **persistent goal "Ralph loop"** (judge model checks goal satisfaction each turn, continues in the same cache-warm session); **`delegate_task`** subagents (isolated context, restricted tools, single+batch+**background**); **iteration budgets**; **Kanban** durable SQLite task board (atomic claims, deps, `dispatch`, LLM `decompose` routed by profile role); **Kanban Swarm** (plan→parallel workers→verifier→synthesizer with a JSON blackboard in task comments); **Mixture-of-Agents** tool; **Programmatic Tool Calling** (`code_execution` writes a script that calls Hermes tools via RPC, collapsing chains, intermediates never enter context); **todo** tool re-injected after compaction.
- **Self-improvement (the headline):** after every turn a **forked agent (memory+skill tools only)** replays the conversation and autonomously writes/updates **skills** (procedural) + **memory** (declarative); the **Curator** (inactivity-triggered, no daemon) auto-archives/consolidates/patches *agent-created* skills, never deletes, snapshots before each run, pinned skills bypass.
- **Skills/plugins:** `SKILL.md` (agentskills.io-compatible); user-local + in-repo; distribution via skills.sh/ClawHub/GitHub "taps"; **bundles** (multi-skill slash aliases); typed **plugin** dirs (memory/model-providers/platforms/context_engine/browser/image/video/cron/observability); ~80 bundled skills.
- **State/memory:** `hermes_state.py` single SQLite (`state.db`, WAL) with dual **FTS5** indexes (full-text + trigram fuzzy session search); session lineage (`parent_session_id`), cross-platform **handoff_state**; curated `MEMORY.md`+`USER.md` (frozen snapshot per session to keep prefix cache warm); pluggable **context engine**; external memory providers (honcho/mem0/hindsight/…); zero-LLM **session search** tool.
- **TUI/UX:** Ink/TS frontend ↔ JSON-RPC bridge ↔ Python `COMMAND_REGISTRY` (single source for CLI/gateway/Telegram/Slack/autocomplete); multiline+autocomplete+interrupt-and-redirect; **clarify** tool (structured multiple-choice questions); web dashboard; Electron desktop; skin/theme engine.
- **Container/process:** **s6-overlay v3** supervision tree in the Docker image; per-profile supervised gateways; tmpfs `/run/service` reconciliation on boot; **process registry** (psutil cross-platform PID mgmt).
- **Verification:** **post-write LSP diagnostics** (run language servers after write/patch); Kanban verifier/synthesizer gate nodes; **kanban-codex-lane** (Codex = untrusted input lane, Hermes owns acceptance + re-runs canonical tests); **OSV** supply-chain audit; **tirith** pre-exec security scanner (cosign-verified).
- **Sandbox/safety:** dangerous-command approval + smart LLM auto-approval + allowlist (`--yolo` bypass); per-subsystem write-approval gate (separates background-review writes); **filesystem checkpoints** (shadow-git snapshot before every mutating turn → `/rollback`, invisible to LLM); **threat-pattern library** (prompt-injection, scoped all/context/strict) over context files/memory/tool-results/skill-installs; **SSRF** + cloud-metadata blocking; path-traversal guards; tool-result delimiter system.
- **Models:** 30+ providers; **credential pool** (multi-key rotation + exhaustion tracking) + ordered cross-provider **fallback**; **Nous Portal** one-sub OAuth (300+ models + Tool Gateway); separate cheap auxiliary model for summarize/judge/review.
- **Run-anywhere:** 6 terminal backends (local/Docker/SSH/Singularity/Modal/Daytona); Modal/Daytona **serverless hibernation** (~zero idle cost).
- **Distinctive:** closed self-improvement loop + curator; multi-platform gateway w/ cross-platform handoff; run-anywhere + hibernation; provider-agnosticism + resilience plumbing; Kanban swarm + durable board; goal Ralph loop; SOUL.md identity + **Icarus** ("train your replacement" — training-data extraction) + trajectory export for RL; **gamified autonomy** (60+ achievement badges as a retention/marketing flywheel).

---

## 4. Claude Flow V3 — capability inventory

**What it is:** `ruvnet/claude-flow` V3 — a **swarm-orchestration framework wrapped around a governance
"control plane"** for Claude Code sessions. ~17 `@claude-flow/*` packages + 15 plugins, MIT, `3.0.0-alpha`
(CLI tagged `3.14.2` — versioning is inconsistent across the tree). It is the only peer here that targets
Orion's own category in name. **Standing caveat for everything below:** the recurring tell across this tree
is *big README → real TypeScript framework → "the real work is in WASM/native"* where the WASM
(`@ruvector/sona`, `agentdb`, plugin `*-wasm`) is **not built or present in-tree** and silently falls back to
JS no-ops. The repo's own honesty tables (`@claude-flow/cli/CLAUDE.md`) recant several headline numbers; treat
all performance and "self-improving" claims as unverified unless marked measured.

- **CLI surface (~44 command groups, ~150+ subcommands):** headless CLI + MCP server are co-primary; **no TUI**
  (zero ink/blessed deps — line-printed output + inquirer prompts). Real, large commands: `swarm`/`hive-mind`,
  `mcp`, `session`, `route`, `autopilot`, `doctor` (1234 LOC), `status --watch`. **Novel/competitive bets:**
  `metaharness` (scores · threat-models · *mints/forks other harnesses* — ADR-150), `eject` (lift the repo into
  a standalone renamed harness), `route` (a live Q-learning model-router), `autopilot` (a Stop-hook
  re-engagement loop ≈ Orion's `/build`/epic-ralph).
- **Orchestration / swarm (`@claude-flow/swarm`):** the "15-agent hierarchical-mesh, Queen-led hive-mind" is
  **15 `AgentState` object literals pushed into a `Map`** (`unified-coordinator.ts:1455`); agents **perform no
  work** — task lifecycle is status-string mutation + event emission, and the executor polls for a
  `task_complete` message **nothing in the package ever constructs** (`:1334`). **No LLM SDK exists anywhere in
  the source tree** (no `@anthropic-ai`/`openai`/`fetch`); every model-calling path is a placeholder string
  (`agentic-flow-agent.ts:752`). The genuinely engineered part is **consensus** — real SHA-256 PBFT + sound Raft
  quorum math + Ed25519 transport — but the *default* path synthesizes peers in-process (`raft.ts:359` "Legacy
  in-process path … fake peer state; `return true`"). Topology is a real adjacency graph that **nothing routes
  on** (delivery is flat broadcast).
- **The guidance "control plane" (`@claude-flow/guidance`) — the headline, detailed below.** Mechanically: it
  *"Sits beside Claude Code (not inside it)"* (`index.ts:4`), compiles `CLAUDE.md`→constitution + shards +
  manifest, retrieves shards by intent classification into the prompt, enforces non-negotiables through
  **deterministic regex/threshold hook gates**, logs every run to a **HMAC-signed ledger**, and evolves the rule
  set via an optimizer. A passive **policy + audit sidecar to Claude Flow's own swarm** — not an ACP driver of
  the developer's agent.
- **Memory (`@claude-flow/memory`):** **one real, measured win** — int8 (3.84× / cosine 0.99999) and RaBitQ
  (32×) quantization. The HNSW graph is real but the marquee **"150x–12,500x search" is recanted by the repo's
  own `CLAUDE.md` ("NOT reproduced"; measured ~1.9–4.7×)** and the runtime path falls through to a brute-force
  SQLite cosine scan because `@ruvector/core` isn't installed. **AgentDB** is a dynamic-import-with-silent-
  fallback shim (degrades to an in-memory `Map`). **Event sourcing** (`@claude-flow/shared/events`) is a real,
  correct append-only store with replay — **but nothing instantiates it at runtime** (used only in tests/
  examples). The audit trail ships as shelf-ware decoupled from execution — the inverse of a proof-gated loop.
- **Learning / self-improvement (`@claude-flow/neural`):** **SONA "self-optimization" computes gradients and
  throws them away** — LoRA matrices are random-initialized once and never updated, so the "measured 0.0043ms
  adapt" times a forward matmul, not learning. The MoE router has real REINFORCE backprop but runs **open-loop**
  (reward path never called in production). The **one genuinely live, persisted learning loop** is a tabular
  **Q-learning model-router** (`cli/src/ruvector/q-learning-router.ts`, real Bellman + prioritized replay,
  persisted to `.swarm/q-learning-model.json`). `ReasoningBank` retrieval (MMR + consolidation) is real but
  **stubbed at the agent boundary** ("for now track statistics"); **no memory→skill promotion** (`promoted:0`).
  The 7 hand-rolled RL algorithms (PPO/DQN/A2C/…) are real and **100% orphan**.
- **Tools / MCP (`/mcp/` + `@claude-flow/mcp`):** an **MCP *server*, not a client** — and the **most
  production-grade subsystem in the tree**. 83 tools (agent/swarm/memory/hooks + neural + an 8-tool **federation**
  consensus surface with `propose`/`vote`/`broadcast`); stdio/http/websocket transports; **OAuth 2.1 + PKCE**
  (prebuilt GitHub/Google), **server-initiated sampling** (real Anthropic provider), token-bucket rate limiting,
  per-call JSON-Schema validation, MCP-spec `2025-11-25`.
- **Hooks (`@claude-flow/hooks`):** a real dynamic event bus (~20 lifecycle event types, 11 workers) but it
  **ships with no built-in handlers** (every registration is in tests); real automation runs via a strong
  **OfficialHooksBridge** mapping internal events onto Claude Code's 9 native `.claude/settings.json` hooks
  (a real `PreToolUse` allow/deny/rewrite policy layer). A *second, unmerged* plugin hook bus lives in
  `@claude-flow/plugins`.
- **Security / safety (`@claude-flow/security`, `@claude-flow/aidefence`):** genuinely strong on the boundary —
  Zod input validation, real path-traversal jail, `shell:false` safe-executor + allow/denylist, bcrypt/HMAC
  crypto, Ed25519 plugin-manifest signing. **Standout & novel:** `tool-output-guardrail.ts` screens *returned*
  tool/MCP/memory content for **second-order (indirect) prompt injection** — OWASP ASI01 — with
  allow/flag/redact/reject; `aidefence` is a real ~24-pattern injection + PII engine. **Hard gaps:**
  `CVE-REMEDIATION.ts` is a static self-attestation doc, **not** an OSV/audit scanner; **SSRF / `169.254.169.254`
  cloud-metadata / egress filtering is entirely absent** (grep-confirmed zero hits).
- **Coordination (`@claude-flow/claims`):** event-sourced **work-stealing + claim coordination** with HLC +
  vector clocks and 17 MCP tools — the most credible in-tree analog to **Orion's beads claim-lock /
  `/coordinate`-`/dispatch`** singleton model.
- **Models / providers (`@claude-flow/providers`):** real multi-vendor adapters (anthropic/openai/google/
  cohere/ollama) + cross-vendor failover/load-balancing — but **no key rotation, no rate-limit backoff**, and
  (per the swarm finding) the execution they front is simulated in this tree.
- **Ecosystem (`/plugins/`):** 3 substantial plugins (agentic-qe ~17K LOC, prime-radiant, a `teammate-plugin`
  bridging Claude Code's native TeammateTool) and **11 WASM-deferred scaffolds** (quantum/hyperbolic/financial/
  healthcare/legal — large Zod/MCP plumbing, zero compiled `.wasm`).
- **Distinctive:** the guidance control-plane vocabulary; `metaharness` (mint/threat-model other harnesses);
  the live Q-learning router; second-order tool-output injection filtering; claim-based coordination; a
  bleeding-edge MCP server. The signature flaw is the gap between framework polish and a **largely-simulated
  execution substrate**.

### Footnote — the `gastown-bridge` plugin (Orion tracks Gastown)

Claude Flow ships a `plugins/gastown-bridge` that namechecks Gastown's whole vocabulary (Beads, Formulas,
Convoys, mayor/polecat/refinery) — but it is **non-functional and mislabels its own target**. Its `gt-bridge.ts`
wraps the *wrong* `gt`: an **Ethereum gas-estimation CLI** (`TxHashSchema = /^0x[a-fA-F0-9]{64}$/`, networks
`mainnet`/`goerli`/`sepolia`, `estimateGas`/`getGasPrice` — `gt-bridge.ts:110,122,573`), the author having
conflated "Gas**town**" with Ethereum "**gas**." Its `bd-bridge.ts` models a "Bead" as an **LLM chat/memory
record** (`threadId` + `embedding` — `bd-bridge.ts:132,135`), not Beads' issue graph. It drives **no real
Gastown deployment** (and misattributes Gastown's authorship). A clean illustration of the marketing-exceeds-
reality pattern, on a competitor we can independently check.

### The guidance control plane vs Orion's proof harness (the load-bearing comparison)

This is why Claude Flow V3 matters to Orion: it is the **first peer to claim Orion's exact thesis in its own
words**, so it is the cleanest available test of whether the distinction Orion draws is real or just framing.
Read in source, the words collide but the mechanisms do not — they are **different categories that share a
vocabulary.**

- **"Proof" is an audit attestation, not a correctness verdict.** `ProofChain` emits a per-run, HMAC-SHA256-
  signed, hash-chained **envelope**; `verify()`/`verifyChain()` check **signature + chain linkage only** — i.e.
  *"this log wasn't tampered with,"* never *"the code is correct"* (`proof.ts:4-11, 202-259`). The one
  correctness-sounding evaluator, `TestsPassEvaluator`, merely **reads a struct the caller populates**, and in
  every non-benchmark path that struct is hardcoded `testResults: { ran: false }` (`ledger.ts:51-65, 274`). The
  headless harness's "tests-pass" assertion is a string grep over stderr (`headless.ts:401`). **Nothing in the
  live path runs tests, probes, or static analysis.** Contrast Orion: *"Correctness is established only when
  independent lines of evidence converge … any single green light is not [proof]."*
- **"Trust" is self-certification — the exact pattern Orion forbids.** `TrustAccumulator` adds **+0.01 to an
  agent's trust on every `allow` gate outcome**; at ≥0.8 the agent reaches the `trusted` tier and earns a **2.0×
  rate-limit multiplier** (`trust.ts:112,124,134`). An agent that keeps passing the *same regex gates it is
  steered by* **buys itself more autonomy.** There is **no withheld verifier and no trust-domain separation** —
  the "homework grader" is the same deterministic rule set the generator follows. This is the textbook case of
  Orion's *"No agent grades its own homework."*
- **All gates are deterministic — and entirely syntactic.** The enforcement (destructive-ops, tool-allowlist,
  diff-size, secrets) is competent, injection-resistant regex/threshold logic, and the ADRs *deliberately reject*
  LLM-as-judge as manipulable — a sound choice for a **guardrail**. But the same ADRs concede the destructive gate
  *"cannot distinguish `rm -rf ./tmp/cache` from `rm -rf /`"* (ADR-G004). No gate understands semantics or
  correctness. `adversarial.ts` is regex prompt-injection/collusion **detection**; `truth-anchors.ts` is a
  human-fed fact store; `conformance-kit.ts`'s flagship "conformance proof" runs a **`SimulatedRuntime` whose
  `invokeModel` returns `'[Simulated inference…]'`** and asserts hardcoded counts of its own mock.

**The honest one-line verdict:** *Claude Flow's guidance plane enforces that the agent followed the rules and
signs a tamper-evident log of what it claims it did; Orion proves the result is correct, with someone other than
the author checking.* Same surface vocabulary — **proof, gates, trust, adversarial, ledger** — but one is
**process governance + audit**, the other is **independent, multi-modal, author-withheld correctness proof**.
The nearest-vocabulary competitor never runs the code, the tests, the probes, or a hazard analysis — which is
the single most useful sentence this comparison produces for Orion's positioning.

### Claude Flow V3 against the A1–A17 gap list

The same lens applied to OpenClaw/Pi/Hermes, now for Claude Flow V3 (HAS / PARTIAL / LACKS; source-grounded):

| Gap | Capability | Claude Flow V3 | Evidence |
|---|---|---|---|
| **A1** | Extension hook bus | **PARTIAL** | Real dynamic `HookRegistry.register` + strong `OfficialHooksBridge` onto Claude Code's 9 hooks; but ships no built-in handlers and has a second, unmerged plugin bus. |
| **A2** | Hot-reload + runtime tool reg | **PARTIAL** | Runtime tool registration HAS (`@claude-flow/mcp` `register/registerBatch` + `tools.listChanged`); hot-reload LACKS (no fs.watch/module hot-swap). |
| **A3** | Machine modes / SDK / headless | **HAS (partial)** | `--format json`, programmatic package exports; **no `stream-json`** streaming mode. |
| **A4** | Forkable sessions | **LACKS** | `session` = list/save/restore/export/import only; no fork/branch/clone. |
| **A5** | TUI ergonomics | **LACKS** | Zero TUI deps; line-printed CLI; `status --watch` is a polling reprint. |
| **A6** | Structured clarify | **LACKS** | Inquirer confirms only; no structured disambiguation flow. |
| **A7** | Background skill/memory writer | **PARTIAL (weak)** | Mechanism real (60s consolidate daemon; post-edit `storePattern`) but autonomous triggers are orphan exports; writes Q-values/memory, **not skills**. |
| **A8** | Skill curator | **PARTIAL** | Real curate/prune/dedup/decay of *memories*; **no memory→skill promotion** (`promoted:0`). |
| **A9** | Cross-harness skill import | **PARTIAL** | `codex` does real CLAUDE.md→AGENTS.md import; `gastown-bridge` *aims* to import Gastown but is mislabeled scaffold. |
| **A10** | Post-write LSP diagnostics | **LACKS** | No LSP/tsserver integration; post-edit guidance is regex lint-style only. |
| **A11** | Differential before/after empirical proof | **PARTIAL (weak)** | `gaia-bench` has ablation toggles; no automated A/B accuracy-delta harness. |
| **A12** | Vendor failover / auth rotation | **PARTIAL** | Cross-vendor failover HAS (`completWithFallback`); **auth/key rotation LACKS**. |
| **A13** | Programmatic tool calling | **HAS** | MCP **server-initiated sampling** + in-process `ToolRegistry.execute()` + `completion/complete`. |
| **A14** | Per-turn FS checkpoints / rollback | **LACKS** | No FS snapshot / git-stash-per-turn / revert worker. |
| **A15** | OSV/CVE audit + binary provenance | **PARTIAL** | Dep-vuln/OSV/SBOM LACKS (`CVE-REMEDIATION.ts` is a static doc); narrow Ed25519 + trust-anchor for plugin manifests HAS. |
| **A16** | SSRF/metadata guards + threat patterns | **PARTIAL (split)** | Threat-pattern libs HAS (two real prompt-injection signature libs incl. **second-order tool-output** filtering); **SSRF/metadata/egress LACKS** (grep-confirmed zero). |
| **A17** | Harness self-operability (doctor/status/backup/logs) | **PARTIAL** | doctor HAS (`--fix` only prints), status HAS (`--watch`); backup LACKS; logs only as `mcp logs`/`agent logs`. |

**New capabilities none of OpenClaw/Pi/Hermes had** (candidate gaps / strategic notes — *not* auto-filed):

1. **Second-order (indirect) prompt-injection filtering of tool/MCP/memory *output*** (OWASP ASI01) — a real,
   distinct surface from Orion's input-side sandboxing. **Mission-aligned**; the strongest new candidate (extends
   #A16; tentatively **#B1**).
2. **Production-grade MCP server with server-initiated sampling + federation consensus tools** — ahead of Orion's
   ACP-client posture; relevant to #A3/#A13 if Orion ever exposes itself as tools.
3. **Event-sourced claim-based coordination** (`claims`, HLC/vector clocks) — an alternative substrate to Orion's
   beads claim-lock; comparison point, not a gap (Orion already has the capability via beads).
4. **`metaharness` — scoring/threat-modeling/minting other harnesses** — out of Orion's stated scope (Orion is not
   a harness factory) but strategically notable as a category Claude Flow is staking.
5. **Live Q-learning model-router** — learned task→agent/model routing from outcomes; a borrowable idea bordering
   #A12, lower priority.

---

## 5. Gap analysis vs Orion V2

Legend — Orion status: **HAVE** (specified in PRD), **PARTIAL** (related but weaker/under-specified), **GAP** (absent), **N/A** (deliberately out of Orion's scope).

| # | Capability (source) | Orion status | Note |
|---|---|---|---|
| Multi-channel chat gateway (OC, H) | — | **N/A** | PRD: single dev/single machine, TUI-first. Out of scope. |
| Persona / SOUL / IDENTITY (OC, H) | — | **N/A** | Orion's Conductor is a role template, not a persona. |
| Gamified achievements (H) | — | **N/A** | Anti-aligned with a reliability harness. |
| Heartbeat proactivity / commitments (OC) | — | **N/A**/PARTIAL | Orion is gated+interactive; not ambiently proactive. |
| Run-anywhere + serverless hibernation (H) | — | **N/A** | Local-first by design (SSH/remote sandbox backend is the one slice worth it — see #G). |
| **Extension event/hook bus** (Pi) | **GAP** | PRD has skills/workflows/primitives + packages, but no lifecycle-hook interception API. **#A1** |
| **`/reload` hot-reload + runtime tool reg** (Pi) | **PARTIAL** | PRD names the "Pi `/reload` analogue" for self-evolution but never specs a dev-time hot-reload. **#A2** |
| **Machine modes `--mode json/rpc` + Go SDK** (Pi) | **PARTIAL** | Orion is an ACP *client*; lacks a structured event stream + embeddable SDK to be driven headlessly. **#A3** |
| **Tree-structured forkable sessions** (Pi) | **GAP** | Context Store is authoritative but has no branch/fork/clone of the conversation for exploring alternatives. **#A4** |
| **TUI ergonomics** (@file/img, `!cmd`, model cycle, steering) (Pi) | **PARTIAL** | ACP-Conductor TUI spec doesn't enumerate these. **#A5** |
| **Structured multiple-choice clarify** (H) | **PARTIAL** | Completeness gate grills via free text; multiple-choice would sharpen A5. **#A6** |
| **Background-review skill/memory writer** (H) | **PARTIAL** | Orion has gated self-evolution (LTM, default off) but no *mechanism* that proposes candidates from a successful run. **#A7** |
| **Skill curator (lifecycle for agent-created skills)** (H) | **GAP** | LTM has compaction/GC notes but no autonomous archive/consolidate/patch lifecycle. **#A8** |
| **Cross-harness skill discovery/import** (Pi, H) | **GAP** | Orion's registry is self-contained; can't reuse `~/.claude`/`~/.codex`/agentskills.io libraries. **#A9** |
| **Post-write LSP diagnostics (fast feedback)** (H) | **GAP** | Proof is behavioral/empirical/hazard; no cheap inline compile/diagnostic signal during generation. **#A10** |
| **Differential before/after empirical proof + visual artifacts** (OC Mantis) | **PARTIAL** | Lookout probes the running artifact; no baseline-vs-candidate diff + screenshot/artifact evidence (matters for brownfield/UI). **#A11** |
| **Vendor-agent failover + auth-profile rotation** (OC, H) | **PARTIAL** | agent-preset registry exists; no failover chain/credential rotation when the driven agent is rate-limited/exhausted. **#A12** |
| **Programmatic Tool Calling for DET chains** (H) | **GAP** | Would collapse multi-step DET tool chains and keep intermediates out of context (budget win). **#A13** |
| **Per-turn filesystem checkpoints + `/rollback`** (H) | **PARTIAL** | Worktree + integration rollback are branch-level; no transparent per-turn shadow-git undo. **#A14** |
| **OSV/CVE audit + tool-binary provenance verify** (H) | **PARTIAL** | dependency-provenance checks existence+provenance; add known-vuln scanning + signed-binary verification. **#A15** |
| **SSRF/metadata egress guards + threat-pattern library** (H) | **PARTIAL** | Sandbox default-deny egress + "render inert"; add named SSRF/metadata blocks + injection threat patterns over all untrusted inputs incl. skill installs. **#A16** |
| **Harness self-operability: `doctor`/`status`/`backup`/`logs` + out-of-band notifications** (OC, H) | **GAP** | Orion instruments itself but has no doctor/backup/restore/notify surface for operating Orion. **#A17** |
| Semantic tool discovery for large catalogs (OC) | **GAP/low** | `tool_search`/`tool_describe` — fold into #A1/#A3 if MCP tool catalog grows. (not separately filed) |
| MCP server (Orion-as-tools) (OC, H) | **GAP/low** | Expose decomposer/proof as MCP tools; candidate, lower priority (noted, not filed). |
| Scheduling/cron for recurring scans/re-proofs (OC, H) | **GAP/borderline** | Useful for the reliability mission; borderline vs interactive local-first. (noted, not filed by default.) |

---

## 6. Filed work

The mission-aligned gaps above (#A1–#A17) are filed as beads issues under a dedicated epic
(**cross-harness feature parity**), each **blocked behind a triage/prioritize decision** so the active
build loop does not auto-build un-prioritized speculative work. See the epic for IDs and current priority.

**From Claude Flow V3 (§4), recorded but NOT auto-filed** — surfaced 2026-06-30, left for triage so the build
loop does not pull speculative work: **#B1 — second-order (indirect) prompt-injection filtering of tool/MCP/
memory output** (OWASP ASI01; the one clearly mission-aligned new candidate, extending #A16). Borderline /
strategic notes, not filed: production-grade MCP server with server-initiated sampling (cf. #A3/#A13), learned
Q-learning routing (cf. #A12), and `metaharness` (out of scope). The biggest takeaway from Claude Flow is **not
a gap at all** — it is the confirmation, in a direct competitor's source, that Orion's *independent,
author-withheld, multi-modal correctness proof* is genuinely differentiated from "compile a policy, gate tool
calls, and sign an audit log."

**Deliberately NOT filed** (out of Orion's stated scope, per `docs/PRD/orion-v2.md` §Out of Scope):
multi-channel messaging gateway, personas/SOUL, gamified achievements, heartbeat/commitments ambient
proactivity, run-anywhere/serverless hibernation, hosted-SaaS execution, **harness-minting / metaharness
tooling**. MCP-server surface, semantic tool discovery, and scheduling/cron are recorded as borderline
candidates the user can promote.
