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
