# Pinned llama.cpp patches

`llama-rpc-streaming-cache.patch` applies only to llama.cpp commit
`c0b97be0b297177a88df08e96c01420976bb18d0` (`b12562`). Build scripts call
`scripts/apply-llama-patches.sh`, which fails closed if the patch no longer
matches that source.

The patch replaces full-tensor temporary allocations in the RPC file cache
with a shared 16 MiB streaming buffer. Cache files are published by rename only
after a complete write; cache hits are streamed and checked for exact size and
FNV-1a hash. A cache write failure no longer aborts an otherwise valid tensor
transfer.

Run the functional gate with:

```sh
LLAMA_SRC=/path/to/pinned/llama.cpp tests/rpc-cache-smoke.sh
```

It exercises a cache miss and hit, then verifies automatic recovery from both
a truncated file and a same-sized file with corrupted contents.

## Isolated smolmaxxing Android runtime

`smolmaxxing-8d12bb1-llama-v0.8.0.patch` is the unmodified MIT-licensed
smolmaxxing patch from commit `8d12bb17fd389c25f3c6167a5b73f93034d6324f`,
with SHA-256
`f0677c48522e1659969998654d83db9ede7652e771e045dbcd35427abd0230bd`.
It applies only to llama.cpp v0.8.0 commit
`8642541b255bfb5dc39cd351a178be09013a55d6`.

It is **not** applied to the stock b12562 Windows/RPC runtime. The release
build compiles a third, Android-only `llama-server` from that exact base. YourLM
selects it only for the built-in recipe marker plus recognised Qwen3.6 MoE metadata on an ARM CPU advertising the dot-product extension. All experimental pager controls are supplied from a
fixed allow-list by YourLM; model files cannot inject environment variables.
The stock engine remains the automatic fallback.

The patch contains far more than a generic speed tweak: expert paging, direct
flash reads, a file-backed repack cache, ARM Q2_K kernels, routing prefetch and
model-specific graph changes. This isolation is intentional. Porting fragments
blindly onto a newer llama.cpp or enabling them for an arbitrary GGUF can
silently produce wrong output.
