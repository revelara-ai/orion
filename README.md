# Orion

**A reliability-first, local-first Go multi-agent orchestration system (cobra + bubbletea) with a strongly opinionated view of proof and validation.**

Orion drives a fleet of specialist agents from a developer's intent to working software, and treats output as **done only when its correctness has been independently proven — never merely asserted.** It is the reliability layer of the agentic software development lifecycle.

> One sentence: *type what you want to build; Orion makes the intent unambiguous, coordinates agents to build it, and only calls it done when behavioral, empirical, and hazard proof converge — then hands you software you can actually operate at 3 a.m.*

## Why Orion exists

Agentic coding tools write code faster than anyone can review it — and quietly degrade reliability: they game their own tests, drift from intent over long runs, emit uninstrumented code, hallucinate dependencies, and leave no author to page when it breaks. The development loop was built for humans; agents break its assumptions in silent, compounding ways.

Orion is a new loop built on one bet: **reliability belongs to the harness, not the model.** Like microservices delivering reliable systems on commodity hardware, Orion produces reliable software on top of fallible, commodity code-generation models — by putting the guarantees in the loop (independent proof, bounded steps, embedded SRE knowledge), not in any single model.

The credibility hinge — and Orion's central rule — is that **no agent grades its own homework**: the agent that generates code is structurally separated from the mechanism that proves it, and proof is, wherever possible, a *deterministic tool* (a shell check, a probe, a test) rather than another LLM's opinion. (Google SRE independently mandates the same "independent harnesses" — see the manifesto's External Corroboration.)

## How it works (the loop)

```
intent ─▶ completeness gate ─▶ executable spec ─▶ decompose (Epic/Tasks)
   ─▶ specialist agents build (sandboxed, one git worktree per task)
   ─▶ multi-modal proof: behavioral (tests + mutation) · empirical (run it, probe it) · hazard (STPA)
   ─▶ deployment bar ─▶ deliver (proof earns the right to ship)
   ─▶ learn (Polaris reliability knowledge in, observed failure modes out)
```

The developer converses with a single orchestrator — the **Conductor** — through a TUI. Behind it, Orion solves the hard problems: efficient agent coordination, context/memory management (countering erosion), durable task tracking, and independent multi-modal proof as the completion criterion.

## Start here

| Document | What it is |
|---|---|
| [docs/MANIFESTO.md](docs/MANIFESTO.md) | The vision and the beliefs — the source of truth everything inherits from. |
| [docs/PRD/orion-v2.md](docs/PRD/orion-v2.md) | The product requirements for Orion V2 (the buildable spec). |
| [docs/INDEX.md](docs/INDEX.md) | Master index of all design docs. |
| [docs/SPEC/](docs/SPEC/) | Component specs: Triad reconciliation, Polaris API contract, worktree/git handling. |
| [docs/prompts/build-orion.md](docs/prompts/build-orion.md) | The provable-implementation loop that builds Orion against its own doctrine. |
| [docs/research/](docs/research/) | Research feeding the design (harness-reliability, Google SRE practices, …). |

## Status

Early, greenfield. **V2.0** is in active development — a Go-greenfield tracer bullet (*idea → proven, runnable, operable service*). Work is tracked in [beads](https://github.com/steveyegge/beads) under the **Orion V2** epic, sliced into proof-gated tasks. Polyglot (TS/Python), brownfield intake, earned autonomy, and self-evolution are phased for V2.1–V2.3.

## Prerequisites

- **git >= 2.28.** Orion's managed-repo foundation (`internal/repo`) initializes repositories with `git init -b main` (the `-b`/`--initial-branch` flag) and relies on the clone default-branch behavior — both introduced in git 2.28. Older git versions will fail to create or sync managed repositories.

## Stack

Go · [cobra](https://github.com/spf13/cobra) (CLI) + [bubbletea](https://github.com/charmbracelet/bubbletea) (TUI) · SQLite (Context Store) + sqlite-vec (memory) · gVisor-sandboxed agents · MCP tools + A2A between agents. Local-first; the one cloud dependency is the **Polaris** reliability platform (controls, knowledge, risk register, STPA primitives).

## License

Orion is licensed under the [Apache License 2.0](LICENSE). Copyright 2026 Revelara AI.

The optional local embedding model ([bge-base-en-v1.5](https://huggingface.co/BAAI/bge-base-en-v1.5), BAAI) is MIT-licensed and downloaded separately — it is not distributed with this repository.

---

*The problem is not the model. The problem is that the development loop hasn't caught up. Orion is the loop that has — and the proof is the right to ship.*
