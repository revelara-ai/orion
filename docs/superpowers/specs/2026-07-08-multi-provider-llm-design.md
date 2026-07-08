# Multi-Provider LLM Support — Design Spec

**Date:** 2026-07-08
**Status:** Approved (brainstorm → design review complete)
**Owner:** josebiro

## Goal

Orion works with any LLM provider — Anthropic (default), OpenAI-compatible endpoints
(LM Studio, Ollama, OpenAI, OpenRouter, vLLM, Groq, Together), and native Gemini —
selected through a configuration facility rather than hardcoded constructors. The
provider abstraction, resilience layer, and config facility are publishable as a
Go module surface with zero Orion-internal dependencies.

## Non-Goals

- Prompt-based tool emulation for models without native tool calling (rejected:
  large reliability surface, contradicts the provable-correctness ethos).
- Per-role model routing (`models: {conductor: X, summarizer: Y}`) — deferred;
  the schema leaves room for it but v1 ships a single active model ref.
- A separate repo/module now. `pkg/` in the orion repo, extractable later.
- Vendor SDKs. All adapters stay hand-rolled HTTP like the existing Anthropic one.

## Package Layout

Everything publishable moves to `pkg/`; a test enforces that `pkg/` imports
nothing under `internal/`.

| Package | Contents | Provenance |
|---|---|---|
| `pkg/llm` | Types, `Provider` interface, capability interfaces, adapters: `anthropic.go` (moved), `openai.go` (new), `gemini.go` (new), `probe.go` (new) | move of `internal/llm` + additions |
| `pkg/llmclient` | Retry/backoff/breaker wrapper | move of `internal/llmclient` (already stdlib-only) |
| `pkg/llm/config` | YAML schema, `LoadFile(path) (Config, error)`, `Build(cfg Config, ref string) (llm.Provider, error)`, `DefaultConfig()` built-in registry | new |
| `internal/llmsetup` | Orion glue: resolves `~/.orion/config.yaml`, applies `ORION_MODEL` / `ANTHROPIC_API_KEY` precedence, returns provider + display label | new, stays internal |

`pkg/llm/config` is path-agnostic and env-prefix-agnostic: it never reads
`ORION_*` env vars or touches `~/.orion` — that policy lives only in
`internal/llmsetup`. This keeps the published surface Orion-free.

The `internal/llm` → `pkg/llm` move updates imports across the repo
(mechanical; ~20 files).

## Config Facility

File: `~/.orion/config.yaml`. API keys are referenced by env-var **name** only —
never stored in the file.

```yaml
model: lmstudio/qwen3-32b        # provider/model ref: <provider-name>/<model-id>
providers:
  anthropic:
    type: anthropic              # anthropic | openai | gemini
    api_key_env: ANTHROPIC_API_KEY
    # base_url: optional override
  lmstudio:
    type: openai
    base_url: http://localhost:1234/v1
    # api_key_env optional — local servers don't need it
  ollama:
    type: openai
    base_url: http://localhost:11434/v1
  gemini:
    type: gemini
    api_key_env: GEMINI_API_KEY
```

Optional per-provider fields:

- `context_window: <int>` — for local models that don't advertise one; surfaces
  through the adapter's `llm.ContextWindow` capability so
  `contextwindow.WindowOf` picks it up instead of the 128K fallback.
- `max_tokens: <int>` — default per-request output cap.

**Model ref grammar:** `<provider-name>/<model-id>`. The model id may itself
contain `/` (OpenRouter ids like `meta-llama/llama-3.3-70b`); split on the
**first** `/` only. A ref without `/` resolves against the default provider
(`anthropic`) for backward compatibility.

**Precedence:** `ORION_MODEL` env > config `model:` > built-in default
(`anthropic/<DefaultAnthropicModel>`).

**Built-in default registry** (used when no config file exists; also merged
underneath a user config so these names always resolve):

- `anthropic` → type anthropic, `ANTHROPIC_API_KEY`
- `ollama` → type openai, `http://localhost:11434/v1`
- `lmstudio` → type openai, `http://localhost:1234/v1`
- `gemini` → type gemini, `GEMINI_API_KEY`

User config entries with the same name override the built-ins.

**Backward compatibility (hard requirement):** no config file +
`ANTHROPIC_API_KEY` set → identical behavior to today, including the TUI's
offline-conductor fallback when the key is absent. `ORION_MODEL=ollama/llama3.3`
works with zero config file via the built-in registry.

## Adapters

Shared pattern (established by `anthropic.go`): hand-rolled HTTP, every request
wrapped in `llmclient.Do` with the same retry policy (3-minute per-attempt
timeout, 3 retries, backoff, breaker), credentials memory-only and never logged,
lossy translation isolated inside the adapter, provider output treated as
GENERATION-tier on ingress.

### OpenAI-compatible (`openai.go`)

- `POST {base_url}/chat/completions` for `Chat`; SSE for `ChatStream`.
- Translation: content blocks ↔ OpenAI messages. `tool_use` → assistant
  `tool_calls`; `tool_result` → role `tool` messages; system → first message
  with role `system`.
- `finish_reason` → `StopReason`: `stop`→`end_turn`, `tool_calls`→`tool_use`,
  `length`→`max_tokens`, `content_filter`→`refusal`, else `StopUnknown`.
- `usage.prompt_tokens/completion_tokens` → `Usage` (cache fields zero unless
  the server reports `prompt_tokens_details.cached_tokens`).
- `GET {base_url}/models` → `Models()`; `Ping()` hits the same endpoint —
  doubles as the "is LM Studio/Ollama actually running?" check.
- `api_key_env` optional; when set, sent as `Authorization: Bearer`.

### Gemini (`gemini.go`)

- `POST .../models/{model}:generateContent` and `:streamGenerateContent` (SSE).
- Translation: `Tools` → `functionDeclarations`; `tool_use` ↔ `functionCall`;
  `tool_result` ↔ `functionResponse`; system → `systemInstruction`.
- Gemini function calls carry no id — the adapter synthesizes stable ids
  (`call_<n>` per response) so the harness's ToolUseID plumbing is unchanged.
- `finishReason` mapping: `STOP`→`end_turn` (or `tool_use` when functionCall
  parts are present), `MAX_TOKENS`→`max_tokens`, `SAFETY`/`PROHIBITED_CONTENT`→
  `refusal`, else `StopUnknown`.
- Known-model context-window table (config `context_window` overrides).
- `Models()` via `GET .../models`; `Ping()` likewise.

## Capability Probe & Degradation

New `llm.Probe(ctx, prov) (ProbeResult, error)` in `pkg/llm/probe.go`:

- One minimal tool-call round-trip (a trivial `echo` tool the model is asked to
  invoke); records whether a well-formed `tool_use` block came back.
- Result cached per provider+model per session (callers keep the value; probe
  itself is stateless).

Policy (wired in `internal/llmsetup` and callers):

- **Chat-only flows** (conversation, summarize/compaction) run regardless.
- **Tool-requiring flows** (`orion change`, conductor agent loop) probe first
  and fail fast with a message naming the model and the missing capability —
  before any work starts.
- TUI shows a warning banner when the active brain fails the tools probe.
- Existing `ErrNotSupported` / `ModelInfo.Tools` machinery carries the signal.

## Call-Site Integration

Replace the three hardcoded `llm.NewAnthropic` sites with `llmsetup`:

- `internal/tui/conversation.go:conductorBrain` — provider + label from
  `llmsetup.Brain()`; offline-conductor fallback preserved when the selected
  provider has no usable credentials/endpoint.
- `cmd/orion/change.go` — same; error message generalizes from "needs
  ANTHROPIC_API_KEY" to naming the selected provider's missing prerequisite.
- `cmd/orion/status.go:brainLabel` — mirrors the same selection.

The TUI `/model` command's rebuild closure goes through the registry
(`llmsetup.Rebuild(ref)`), and `/model` listing aggregates `Models()` across
all configured providers, prefixed with provider names.

## Error Handling

- Unknown provider in ref → error listing the configured provider names.
- `api_key_env` set but env var empty → error naming the exact env var.
- Unreachable `base_url` → llmclient retries, then a clear
  "cannot reach <name> at <base_url> — is it running?" message.
- Malformed YAML → error with file path and line from yaml.v3.

## Testing

- **Adapter translation tests:** `httptest` servers with golden
  request/response JSON per adapter (mirrors `anthropic_test.go` /
  `anthropic_stream_test.go`), covering tool round-trips, streaming assembly,
  stop-reason and usage mapping.
- **Config tests:** parse, defaults merge, precedence (`ORION_MODEL` > file >
  default), model-ref grammar (first-slash split, no-slash fallback), key-env
  indirection errors.
- **Probe tests:** tool-capable and tool-incapable fake providers.
- **Boundary test:** `pkg/` imports nothing under `internal/` (AST or
  `go list` based).
- **Acceptance:** `orion change` end-to-end against LM Studio or Ollama with
  only a config file edit; zero-config Anthropic path unchanged.

## Risks

- **Import churn:** the internal→pkg move touches ~20 files; purely mechanical,
  done as its own commit so review is trivial.
- **OpenAI-compat dialect drift:** LM Studio/Ollama diverge from OpenAI in
  corners (e.g. `tool_choice` support, usage-in-stream). Adapter sticks to the
  widely-implemented subset; golden tests encode the exact wire shape.
- **Gemini id synthesis:** synthesized functionCall ids must stay stable within
  a turn or tool dispatch breaks; covered by a dedicated test.
