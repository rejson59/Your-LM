#!/usr/bin/env bash
# Apply the small, audited YourLM fixes on top of the pinned llama.cpp source.
set -euo pipefail

: "${LLAMA_SRC:?Set LLAMA_SRC to the pinned llama.cpp checkout}"
repo=$(cd "$(dirname "$0")/.." && pwd)
src=$(cd "$LLAMA_SRC" && pwd)
patch="$repo/patches/llama-rpc-streaming-cache.patch"
[[ -s "$patch" ]] || { echo "Missing llama.cpp RPC cache patch: $patch" >&2; exit 1; }

if git -C "$src" apply --reverse --check "$patch" >/dev/null 2>&1; then
  echo "llama.cpp RPC cache patch is already applied"
elif git -C "$src" apply --check "$patch"; then
  git -C "$src" apply "$patch"
  echo "Applied bounded-memory RPC cache patch"
else
  echo "Pinned llama.cpp no longer matches the audited RPC cache patch" >&2
  exit 1
fi
