---
title: Harness Feature Comparison — OpenClaw, Pi, Hermes → Orion
status: research
author: Joseph Bironas (with agent research)
created: 2026-06-21
sources:
  - local install OpenClaw (CLI 2026.4.10 / npm openclaw 2026.5.28 / clawhub 0.4.0)
  - local install Pi (@earendil-works/pi-coding-agent 0.79.8, pi.dev)
  - local install Hermes (0.16.0 / build 2026.6.5, Nous Research)
  - docs/PRD/orion-v2.md
related_beads_epic: <set after filing — cross-harness feature parity>
---

# Harness Feature Comparison: OpenClaw · Pi · Hermes → Orion

> Goal: inventory the features/capabilities of three peer agentic harnesses, then identify
> the gaps worth closing in Orion. Each harness was researched from its **local installation**
> (binaries + full source/skills), not from marketing. Orion status is judged against
> `docs/PRD/orion-v2.md`. The "necessary additions" are filed as beads issues (see end).

## TL;DR — what each harness *is*

| Harness | One-liner | Primary surface | Distinctive bet |
|---|---|---|---|
| **OpenClaw** (2026.5.28, MIT, `openclaw/openclaw`) | Personal AI-assistant **gateway**; coding is one skill among many | 20+ chat channels + TUI + dashboard + companion nodes | Always-on multi-channel gateway, persona+self-curated memory ("SOUL"), 46-provider failover, ClawHub registry, Mantis live-transport verification |
| **Pi** (0.79.8, MIT, pi.dev, Mario Zechner) | Minimal **terminal coding harness**, "primitives over features" | Chat-first TUI + print/json/rpc + embeddable SDK | 4 default tools + everything-else-as-editable-extension, npm/git packages, `/reload` hot-reload, ~30-hook extension bus, tree-structured forkable sessions |
| **Hermes** (0.16.0, MIT, Nous Research; lineage: OpenClaw fork) | **Self-improving** autonomous agent that "runs anywhere" | Multi-platform gateway + TUI + ACP + dashboard | Closed self-improvement loop (writes its own skills/memory), Kanban swarm, run-anywhere backends w/ serverless hibernation, aggressive supply-chain hardening, gamified autonomy |

**Where Orion sits:** Orion V2 is a **reliability-first, proof-gated, local-first control plane** that
*drives the developer's own coding agent over ACP* — it is not an LLM client. Its differentiator is the
independent multi-modal proof harness (behavioral + empirical + hazard), trust-domain separation
("no agent grades its own homework"), and the Polaris reliability loop. The three peers above are mostly
**model-clients with rich UX/extensibility/multi-channel reach**; Orion already exceeds them on
*verification rigor and coordination discipline*, but trails on *extensibility ergonomics, session UX,
fast-feedback signals, self-improvement mechanics, and harness self-operability*. Those trailing areas are
the gap list.

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

## 4. Gap analysis vs Orion V2

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

## 5. Filed work

The mission-aligned gaps above (#A1–#A17) are filed as beads issues under a dedicated epic
(**cross-harness feature parity**), each **blocked behind a triage/prioritize decision** so the active
build loop does not auto-build un-prioritized speculative work. See the epic for IDs and current priority.

**Deliberately NOT filed** (out of Orion's stated scope, per `docs/PRD/orion-v2.md` §Out of Scope):
multi-channel messaging gateway, personas/SOUL, gamified achievements, heartbeat/commitments ambient
proactivity, run-anywhere/serverless hibernation, hosted-SaaS execution. MCP-server surface, semantic
tool discovery, and scheduling/cron are recorded as borderline candidates the user can promote.
