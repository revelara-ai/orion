# Semantic memory

Orion's memory recall is **keyword + heat** out of the box — no model, no
downloads, works everywhere. *Semantic* recall adds embedding similarity (a
paraphrase recalls what keywords miss) via a fully in-process, CGO-free
embedder (GoMLX + ONNX): nothing leaves your machine. It is **on by default
once provisioned** — the only step is downloading the model; opt out any time
with `ORION_MEMORY_EMBEDDER=off`.

## Provisioning

The embedder needs the `bge-base-en-v1.5` ONNX export (~416 MB) and its
tokenizer. One command provisions both, verified against pinned SHA-256
checksums (idempotent — re-running skips verified files; a corrupted file is
re-downloaded):

```sh
orion model fetch                # installs under <data-dir>/models
orion model fetch --dir /custom  # or anywhere you like
```

That's it — with the assets under `<data-dir>/models`, semantic recall is
active on the next run. A custom `--dir` needs the explicit env config below.

`orion doctor` reports the state: `ok` when disabled, on, or simply not yet
provisioned (with the enable hint); `warn` only when `ORION_MEMORY_EMBEDDER`
is explicitly configured but the assets are missing — recall silently
degrades to keyword+heat in that state, and the check tells you to run
`orion model fetch`.

## Environment variables

| Variable | Meaning |
|---|---|
| `ORION_MEMORY_EMBEDDER` | unset = on when provisioned (default); `off`/`none`/`0` disables; `local` forces the in-process embedder |
| `ORION_MEMORY_MODEL_PATH` | Directory holding `model.onnx` + `tokenizer.json` |
| `ORION_MEMORY_EMBEDDING_MODEL` | Model name (default `bge-base-en-v1.5`) |

## Licensing

`bge-base-en-v1.5` is published by BAAI under the **MIT license**
(<https://huggingface.co/BAAI/bge-base-en-v1.5>). Orion downloads it at
provisioning time and never redistributes it.
