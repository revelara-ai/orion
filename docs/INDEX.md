# Orion Documentation

## User docs

- **[Getting started](./getting-started.md)** — install → first proven build → brownfield change.
- **[CLI reference](./reference/cli.md)** — the full `orion` command surface.
- **[Environment reference](./reference/environment.md)** — every `ORION_*` variable with default + effect.
- **[Config example](./examples/orion-config.example.yaml)** — annotated configuration.
- **[Semantic memory](./semantic-memory.md)** — optional embedding-based recall: provision once (`orion model fetch`), on by default after that.

## Design

- **[The Orion Manifesto](./MANIFESTO.md)** — the vision and beliefs everything else inherits from: reliability comes from the loop, not the model, and no agent grades its own homework.
- **[Orion V3 PRD](./PRD/orion-v3.md)** — the current direction: the Anchored Module Pipeline (V2 retained as the migration oracle).
- **[Orion V2 PRD](./PRD/orion-v2.md)** — the proven spine: a local-first, TUI-driven agentic harness whose completion criterion is independent, multi-modal proof of correctness.
- **[Architecture decision records](./adr/)** — the load-bearing design decisions, one file each.

The acceptance harness (`test/acceptance`) encodes the product target as executable
predicates; deferred surfaces are tracked by an honesty ratchet that forces a
re-score the moment one turns green.
