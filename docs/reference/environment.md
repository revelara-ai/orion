# Environment variables

Everything is optional; unset means the documented default. Variables marked
*(advanced)* tune internals — the defaults are the supported configuration.

## Core

| Variable | Default | Effect |
|---|---|---|
| `ORION_DATA_DIR` | `~/.orion` | Data dir: Context Store, credentials, config, transcripts. |
| `ORION_MODEL` | anthropic default | Model ref `provider/model` (e.g. `gemini/gemini-2.5-flash`, `lmstudio/<id>`). Bare ids resolve against the anthropic provider. |
| `ORION_AGENT` | unset | Opt-in vendor coding agent for generation (`claude`/`gemini`/`codex` preset, driven over ACP). Unset = native harness / deterministic fixture. `orion run` never silently spawns an agent. |
| `ORION_OUTPUT_DIR` | unset | Export the proven artifact tree on deliver (code always also lives in the build dir + store). |

## Budgets & resilience

| Variable | Default | Effect |
|---|---|---|
| `ORION_BUDGET_MAX_TOKENS` | unlimited | Token ceiling; the loop halts + escalates at the ceiling. |
| `ORION_BUDGET_MAX_DOLLARS` | unlimited | Dollar ceiling (needs provider pricing; see or-v9f.28 for current caveats). |
| `ORION_BUDGET_MAX_WALL_MINUTES` | unlimited | Wall-clock ceiling per run. |
| `ORION_RETRY_BUDGET` | 20 | Stack-wide retry ceiling per operation (turn / build / change) — retries anywhere draw from ONE pool (anti-amplification). |
| `ORION_LOOP_BREAKER_TURNS` | 3 | Consecutive provider-failed turns before the session circuit breaker opens (half-open probe per minute; `/model` resets). |
| `ORION_MAX_INFLIGHT_LLM` | 4 | Process-wide concurrent model-call cap; background/shadow traffic sheds first. |
| `ORION_MAX_AGENTS` | 3 | Dispatcher concurrency for subagent work. |

## Proof & gates

| Variable | Default | Effect |
|---|---|---|
| `ORION_REGRESSION_SCOPE` | scoped | `full` forces the whole suite in the brownfield regression gate (default: changed packages + blast radius). |
| `ORION_ALIGN_GATE` | log-only | `block`: severity-tiered AlignmentGate — a corroborated high-severity intent violation removes the green light (never adds one). |
| `ORION_MODULE_PROPOSER` | off | `shadow`: semantic ModuleProposer runs alongside the oracle (measured, never drives). `live`: proposer drives through the deterministic trust wall, oracle fallback. |
| `ORION_DESIGN_PROOF` | advisory | `block`: every control-plane design-proof model failure fails the lane (per-model rollout otherwise). |
| `ORION_ISSUE_REVIEW` | advisory | `block`: a corroborated high-severity issue-set finding (e.g. a cross-issue contradiction) blocks the plan until the spec is patched. |
| `ORION_CHANGE_ATTEMPTS` | 3 | Self-correction budget for `orion change`: total generator attempts (digest-fed retries; the oracle never changes). |
| `ORION_RUN_ACCEPTANCE` | true | `false` skips the North-Star acceptance harness (red-by-design tracker, not a merge gate). |
| `ORION_PROOF_RUN_COUNT` *(advanced)* | 1 | Empirical probe repetitions (min 1). |
| `ORION_PROOF_TIME_SCALE` *(advanced)* | 1.0 | Scales proof deadlines for slow machines (calibration never re-anchors a spec). |
| `ORION_EXEC_CASES` *(advanced)* | shadow | Exec-case obligation gating rollout control. |
| `ORION_OBLIGATION_RUN` / `ORION_OBLIGATION_PASS` *(advanced)* | — | Obligation-vocabulary phase controls. |

## Sandbox & git

| Variable | Default | Effect |
|---|---|---|
| `ORION_SANDBOX_ISOLATION` | auto | Force a sandbox backend (`bwrap`/`none`). `none` is unsandboxed — trusted input only. |
| `ORION_NET` | deny | Sandbox egress policy for generation. |
| `ORION_GIT_DELIVERY` | off | Opt-in: auto-commit delivered code to the managed repo (never the harness's own repo). |
| `ORION_GIT_PR` | off | Opt-in: open a real GitHub PR on deliver (else a PR-ready artifact + the exact commands). |
| `ORION_BROWNFIELD_TARGET` *(advanced)* | unset | Override the brownfield target repo. |

## Memory & knowledge

| Variable | Default | Effect |
|---|---|---|
| `ORION_ELICITATION` | checklist | `grill` lets the LLM grill drive open-ended elicitation (V3 Step 5); the completeness checklist is demoted to a reliability floor that still must resolve before ratification. Fail-open: a grill error reverts to the checklist. |
| `ORION_SCOPE_LEASE` | observe | `enforce` makes file-scope leases binding on generation: write tools refuse out-of-lease paths and out-of-scope artifacts are routed to refinement (Reject on repeat). Default observes + records the actually-written scope, which integration leases already prefer. |
| `ORION_ALIGN_MODEL` | session brain | `provider/model` runs the alignment judge on an INDEPENDENT model from the generator (judge independence by model, not just criteria). Unbuildable falls back to the session brain with a warning. |
| `ORION_MODEL_<ROLE>` | session brain | Per-role model routing (or-kzf.4): `ORION_MODEL_REVIEW=anthropic/claude-small` routes that role to a cheaper model. Roles: GENERATE, ALIGN, PROPOSE, GRILL, REVIEW, DISTILL, DESIGN. File equivalent: harness `models.yaml` (`roles:` map). Env wins; unbuildable falls back with a warning; routing is recorded per project. |
| `ORION_AGENT` | fixture | Vendor coding agent, now an ordered failover chain: `claude,gemini,codex` — rate-limit/overload/quota/hang/refusal advances to the next entry with a visible notice. |
| `ORION_AGENT_TURN_TIMEOUT` | 20m | Per-turn deadline for a vendor-agent generation turn — a hung agent can no longer wedge an unattended run. |
| `ORION_CHECKPOINT_EVERY` / `ORION_CHECKPOINT_INTERVAL` | N/4 clusters / off | Milestone-checkpoint cadence: every k completed clusters and/or a wall-clock interval; each emits a trajectory digest (coverage-so-far vs schedule, concerns, escalations) as a Checkpoint phase + notify kind=checkpoint. |
| `ORION_CHECKPOINT_MODE` | advisory | `pause-for-ack` files an inbox escalation at each checkpoint and refuses further dispatch until it is answered. |
| `ORION_BUDGET_MAX_DOLLARS` | off | Now REAL (or-v9f.28): turns are priced per provider/model (all four token classes) into a persistent per-project ledger; the ceiling evaluates cumulative project spend across restarts. Unknown/local models book tokens-only, flagged unpriced. |
| `ORION_ANTHROPIC_CONTEXT_EDITS` | off | `1` opts the Anthropic path into server-side context-management edits (beta header + clear_tool_uses edit). Provider-agnostic core behavior is unchanged; verify live before relying on it. |
| `ORION_WORKSPACE_WRITES` / `ORION_WORKSPACE_ROOT` | anchored to cwd | Workspace write anchoring (or-1cv): write_file/edit_file refuse paths outside the workspace root (traversal + outside-absolute). `unrestricted` restores trusted anywhere-writes; `ORION_WORKSPACE_ROOT` relocates the anchor. Reads stay unrestricted. |
| `ORION_BASELINE_MEMO` | on | `off` disables green-baseline memoization in the regression gate (cache keyed by tree-hash + scope + skip + go version; GREEN only; 7-day TTL; dirty trees never hit; evidence stamps the cache source). |
| `ORION_GATE_TEST_TIMEOUT` | 20m | Per-binary `go test -timeout` for regression-gate runs; a timed-out package retries once solo before the baseline reds (busy machine ≠ red package). Suites serialize machine-wide on ~/.orion/proof.lock. |
| `ORION_HARNESS_DIR` | `~/.orion/harness` | Externalized, reviewable harness config: `generation_preamble.tmpl`, `checklists.yaml`, `rules.md` — edits change behavior without a rebuild; invalid files warn + fall back (see `orion doctor`). |
| `ORION_MEMORY_DISTILL` | off | `1` enables the LLM distillation pass: transferable rules from refinement trajectories, written as generation-tier candidates (opt-in). |
| `ORION_MEMORY_EMBEDDER` | off | `gomlx` enables pure-Go semantic recall (opt-in). |
| `ORION_MEMORY_EMBEDDING_MODEL` / `ORION_MEMORY_MODEL_PATH` / `ORION_EMBED_MODEL_DIR` | bge-base defaults | Embedding model selection/location. |
| `ORION_POLARIS_MCP_URL` | token's endpoint | Override the revelara.ai MCP endpoint. |
| `ORION_POLARIS_URL` | — | Legacy REST endpoint override (being retired). |
| `ORION_FIZZBEE_DIR` | `~/.orion/tools/fizzbee` | FizzBee model-checker dist for design proofs (skip-if-absent). |
| `ORION_NOTIFY_WEBHOOK` | unset | Fire-and-forget run notifications (deliver/escalate) to a webhook. |
| `ORION_TRACKER_BACKEND` | none | External tracker projection backend (`beads`). |

## Resume & internal *(advanced)*

`ORION_RESUME_DIR` / `ORION_RESUME_TASK` / `ORION_RESUME_MARKER` /
`ORION_RESUME_HELPER` (crash-resume plumbing), `ORION_ROLE_FILE` (agent role
injection), `ORION_WORKOS_CLIENT_ID` (OAuth client override),
`ORION_COORDINATOR_API_KEY` (coordinator auth). Test-only variables
(`ORION_TEST_*`) are not configuration.
