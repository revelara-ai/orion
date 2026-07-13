# Semantic memory (opt-in)

Orion's memory recall is **keyword + heat** by default — no model, no
downloads, works everywhere. Opting into *semantic* recall adds embedding
similarity (a paraphrase recalls what keywords miss) via a fully in-process,
CGO-free embedder (GoMLX + ONNX): nothing leaves your machine.

## Provisioning

The embedder needs the `bge-base-en-v1.5` ONNX export (~416 MB) and its
tokenizer. One command provisions both, verified against pinned SHA-256
checksums (idempotent — re-running skips verified files; a corrupted file is
re-downloaded):

```sh
orion model fetch                # installs under <data-dir>/models
orion model fetch --dir /custom  # or anywhere you like
```

Then enable it:

```sh
export ORION_MEMORY_EMBEDDER=local
export ORION_MEMORY_MODEL_PATH="$HOME/.orion/models"   # wherever you fetched
# optional: ORION_MEMORY_EMBEDDING_MODEL=bge-base-en-v1.5 (the default)
```

`orion doctor` reports the provisioning state: `ok` when off (the deliberate
default) or provisioned; `warn` when `ORION_MEMORY_EMBEDDER` is set but the
assets are missing — recall silently degrades to keyword+heat in that state,
and the check tells you to run `orion model fetch`.

## Environment variables

| Variable | Meaning |
|---|---|
| `ORION_MEMORY_EMBEDDER` | `local` enables the in-process embedder; unset = keyword+heat only |
| `ORION_MEMORY_MODEL_PATH` | Directory holding `model.onnx` + `tokenizer.json` |
| `ORION_MEMORY_EMBEDDING_MODEL` | Model name (default `bge-base-en-v1.5`) |

## Licensing

`bge-base-en-v1.5` is published by BAAI under the **MIT license**
(<https://huggingface.co/BAAI/bge-base-en-v1.5>). Orion downloads it at
provisioning time and never redistributes it.
