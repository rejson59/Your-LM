#!/usr/bin/env bash
# Apply the audited smolmaxxing phone-runtime patch to its exact llama.cpp base.
# This is deliberately separate from YourLM's stock/RPC runtime: the pager is
# experimental, Linux/Android-specific and model-layout-sensitive.
set -euo pipefail

: "${SMOL_LLAMA_SRC:?Set SMOL_LLAMA_SRC to llama.cpp v0.8.0}"
repo=$(cd "$(dirname "$0")/.." && pwd)
src=$(cd "$SMOL_LLAMA_SRC" && pwd)
commit=8642541b255bfb5dc39cd351a178be09013a55d6
patch="$repo/patches/smolmaxxing-8d12bb1-llama-v0.8.0.patch"
patch_sha256=f0677c48522e1659969998654d83db9ede7652e771e045dbcd35427abd0230bd

[[ $(git -C "$src" rev-parse HEAD) == "$commit" ]] || {
  echo "Expected llama.cpp v0.8.0 ($commit) for the smolmaxxing runtime" >&2
  exit 1
}
[[ -s "$patch" ]] || { echo "Missing pinned smolmaxxing patch: $patch" >&2; exit 1; }
actual=$(sha256sum "$patch" | cut -d' ' -f1)
[[ "$actual" == "$patch_sha256" ]] || {
  echo "Pinned smolmaxxing patch digest changed ($actual)" >&2
  exit 1
}

if git -C "$src" apply --reverse --check "$patch" >/dev/null 2>&1; then
  echo "smolmaxxing patch is already applied"
elif git -C "$src" apply --check "$patch"; then
  git -C "$src" apply "$patch"
  echo "Applied pinned smolmaxxing 8d12bb1 expert-pager patch"
else
  echo "llama.cpp v0.8.0 no longer matches the pinned smolmaxxing patch" >&2
  exit 1
fi
