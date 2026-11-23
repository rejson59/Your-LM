# Qwen3.6-35B-A3B Abliterated — YourLM Balanced Phone

YourLM RC15 introduced, and RC16 retains, one recommended middle ground instead
of asking the user to choose between a small fast model and a larger slow model.
Model weights are **not** bundled with YourLM. RC16 adds local hardware tests;
it does not silently replace this audited recipe with an unmeasured 80B model.

## Reproducible source identity

The built-in recipe is pinned to Hugging Face repository
`huihui-ai/Huihui-Qwen3.6-35B-A3B-abliterated-MTP-GGUF` at immutable revision:

`1fbb5305060b5365c84879612a619d0d2c0b137a`

Sources:

| Role | File | Bytes | Git LFS SHA-256 |
|---|---|---:|---|
| non-expert tensors and metadata | `Huihui-Qwen3.6-35B-A3B-abliterated-ggml-model-Q4_K.gguf` | 21,712,409,824 | `63b75afb4b68fc61059c78115b580037074de88146e0762f4a496081e410cf81` |
| expert tensors | `Huihui-Qwen3.6-35B-A3B-abliterated-ggml-model-Q2_K.gguf` | 13,246,129,376 | `85f446fff19406aa81bd8333433fa11921038ba1b80523c2432d82623b73fb73` |

The source model is advertised by its publisher as abliterated/refusal-reduced.
YourLM does not rename the separate aligned hyperionaidev phone model and does
not claim that abliteration makes a model more intelligent.

## Assembly rule

`host/model_recipe.go` performs a resumable, range-based assembly:

1. Read bounded GGUF headers before large transfers.
2. Require the exact pinned revision, paths, sizes and LFS object identifiers.
3. Require `qwen35moe` for both files.
4. Require equal tensor name/dimension schemas.
5. Require exact tokenizer, `qwen35moe.*`, and checkpoint-relevant `general.*`
   metadata (except the expected quantization file type).
6. Copy every non-expert tensor, including output/head and MTP tensors, from
   Q4_K.
7. Replace only tensors whose names contain `_exps.` with the matching tensor
   from the `MOSTLY_Q2_K` variant. That standard policy uses Q2_K for gate/up
   and Q3_K for FFN-down; the exact per-tensor type remains in the GGUF.
8. Store non-experts first and experts last, with valid GGUF alignment.
9. Add `yourlm.*` metadata containing the recipe, source revision, source
   SHA-256 values, and mixed-quantization description.
10. Validate the completed GGUF and calculate a normal whole-file SHA-256. The
    digest is shown in the UI and saved next to the model as `.sha256`.

The downloader never keeps complete Q2_K and Q4_K source files. Completed
ranges and the recipe fingerprint are saved in a private resume plan. A changed
source identity, schema, metadata set, output header, or plan invalidates the
resume state.

## Runtime isolation

Windows and RPC continue to use llama.cpp b12562 commit
`c0b97be0b297177a88df08e96c01420976bb18d0` and YourLM's bounded RPC-cache
patch.

The Android APK additionally contains `libylm_smol.so`, built from llama.cpp
v0.8.0 commit `8642541b255bfb5dc39cd351a178be09013a55d6` plus the unmodified
smolmaxxing patch from commit
`8d12bb17fd389c25f3c6167a5b73f93034d6324f`. The checked-in patch SHA-256 is:

`f0677c48522e1659969998654d83db9ede7652e771e045dbcd35427abd0230bd`

YourLM selects that executable only when all of these conditions hold:

- the host is Android;
- the executable is present;
- `/proc/cpuinfo` advertises ARM dot-product support;
- bounded GGUF metadata identifies `qwen35moe` with the expected MoE shape;
- the model is at least 10 GiB;
- no RPC worker is attached.

Otherwise the stock engine is used automatically. Filenames alone can never
enable model-specific kernels.

## Balanced defaults

- two highest-frequency CPU cores for inference, strict affinity;
- other cores available to asynchronous UFS readers;
- expert pool 48 / 64 / 80 / 96 according to physical RAM;
- context 2,048 / 3,072 / 4,096 / 8,192 according to physical RAM;
- Q8_0 KV cache, Flash Attention, batch 64, micro-batch 4;
- expert prefetch and cache-routing bias `0.005` (the reference report traded roughly +0.7% perplexity for higher cache locality; it is not lossless);
- private, model-identity-keyed repack cache;
- launch refusal if a new cache cannot fit with a safety reserve;
- process stop after three consecutive samples below 400 MiB available RAM.

MTP/speculative decoding is deliberately **off**. The published smolmaxxing
E101 measurement was 42.4 tok/s without speculation, 38.0 tok/s at depth 1,
and 29.4 tok/s at depth 2. YourLM will not enable a depth from the model name or
publisher example; it needs a faster physical-device measurement first.

## Honest limits

The pager and mixed recipe reduce resident-memory pressure; they do not change
the model's underlying capability. The first direct-phone launch needs enough
free UFS storage for the model plus a repack cache. Throughput, thermals, flash
latency, and profitable MTP depth on a specific Redmi remain hardware tests,
not release claims.
