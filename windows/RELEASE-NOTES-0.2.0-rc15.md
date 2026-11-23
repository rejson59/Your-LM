# YourLM 0.2.0-rc15 — Qwen3.6 Balanced Phone

RC15 adds one practical recommended model path for direct Android use while
preserving the proven Windows/RPC runtime.

## One recommended middle ground

- Adds **Qwen3.6-35B-A3B Abliterated · Balanced Q2/Q4** to the model screen.
- Builds one experts-last GGUF from immutable Huihui Q4_K and Q2_K sources:
  non-experts/head/MTP come from the Q4_K variant; expert tensors come from the
  MOSTLY_Q2_K variant (its normal Q2_K/Q3_K policy).
- Streams only the needed ranges, resumes interrupted work, validates exact
  tokenizer/checkpoint metadata and tensor schemas, and calculates a
  publishable whole-file SHA-256 after assembly.
- Does not bundle model weights. The source model is refusal-reduced; YourLM
  does not call the separate aligned phone GGUF uncensored and does not claim
  that abliteration increases intelligence.

## Auditable low-RAM Android runtime

- Packages a separately named Android ARM64 pager runtime built from exact
  llama.cpp v0.8.0 commit `8642541` and the MIT smolmaxxing `8d12bb1` patch.
- Keeps llama.cpp b12562 unchanged for Windows, generic Android fallback, and
  RPC workers.
- Enables the pager only for verified Qwen3.6 MoE metadata on a compatible ARM
  phone; a filename cannot activate model-specific code.
- Automatically uses the fastest two cores, a RAM-sized expert pool,
  conservative context/KV settings, UFS prefetch, disk-space checks, and a
  low-memory stop guard.
- Keeps MTP speculation disabled. Published measurements showed depths 1 and 2
  slower than baseline; Redmi testing is still required before enabling it.

## Operational notes

The first phone-local start needs substantial free storage for the GGUF and its
private repack cache. Building the cache takes time once. Speed, thermals, peak
RAM, and MTP depth depend on the physical Redmi and are not promised by this
release.

Full recipe, source hashes, profile values, fallbacks, and limitations are in
`docs/QWEN36-BALANCED.md` in the source repository.
