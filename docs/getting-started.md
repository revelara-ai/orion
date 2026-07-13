# Getting started

This walkthrough takes you from install to a **proven, delivered** service.

## Install

See the [README Install section](../README.md#install). Short version:

```bash
git clone https://github.com/revelara-ai/orion.git && cd orion
make build && make install     # → ~/.local/bin/orion
orion doctor                   # 'fail' breaks the exit code; 'warn' is advisory
```

You need Go (version in `go.mod`), git ≥ 2.28, and — on Linux — bubblewrap
for the generation/proof sandbox. A model comes from `ANTHROPIC_API_KEY`,
`GEMINI_API_KEY` (select with `ORION_MODEL=gemini/<model>`), or a local
OpenAI-compatible server configured in `~/.orion/config.yaml`.

## Your first proven build (greenfield)

```bash
# 1. Submit an intent. Orion opens a spec with the decisions it needs answered.
echo "an HTTP service that returns the current time as JSON" | orion submit --non-interactive

# 2. Answer the blocking decisions (or run the TUI, which grills you instead):
orion answer response_format json
orion answer timezone UTC
orion answer port 8080
orion answer route /time

# 3. Ratify: preview the assumptions, approve them, accept the spec.
orion spec show
orion spec approve

# 4. Build + prove. Orion decomposes the spec into a task DAG, generates each
#    module in a sandboxed worktree, and proves it three ways — behavioral
#    (tests scored by mutation), empirical (runs the binary and probes it),
#    hazard (STPA controls present). Done = all three converge.
orion run

# 5. Inspect the result: the proof, the plan, the delivery + runbook.
orion proof show
orion deliver show
```

What you get is not "the agent says it works": the artifact ran, its port
answered, its tests caught injected faults, and the runbook for the 3 a.m.
page is part of the deliverable.

## Changing an existing repo (brownfield)

```bash
cd your-repo
orion change "add a 5s timeout to the outbound HTTP call in fetchStatus"
```

Orion edits a fresh worktree (your tree is never touched), proves the change
did no harm (green-before → green-after regression gate), attaches the
before/after behavioral evidence to a PR-ready artifact, and commits on a
review branch only when the proof holds.

## The interactive way

Run `orion` with no arguments for the TUI: the Conductor grills your intent
into an unambiguous spec, streams the build phases live, and asks approval
before any tool acts on your environment. `/help` lists the commands
(`/fork`, `/tree`, `/compact`, `/model`, …).

## Where things live

- `~/.orion/` — the data dir (Context Store DB, credentials, config,
  transcripts). Override with `ORION_DATA_DIR`.
- Generated projects: managed repos under the data dir; export with
  `ORION_OUTPUT_DIR` or read `orion deliver show`.

Next: the [CLI reference](reference/cli.md) and the
[environment variable reference](reference/environment.md).
