#!/usr/bin/env bash
# Refuse Android packages containing the non-PIE generic Linux executables that
# broke v0.1.1. Every executable payload must be an ARM64 Android PIE.
set -euo pipefail

if (($# == 0)); then
  echo "usage: $0 ELF [...]" >&2
  exit 2
fi

reader=${READELF:-}
if [[ -z "$reader" ]]; then
  if [[ -n "${ANDROID_NDK_HOME:-}" ]]; then
    reader=$(find "$ANDROID_NDK_HOME/toolchains/llvm/prebuilt" -path '*/bin/llvm-readelf' -type f -print -quit)
  fi
  reader=${reader:-$(command -v llvm-readelf || command -v readelf || true)}
fi
[[ -x "$reader" ]] || { echo "llvm-readelf/readelf is required" >&2; exit 2; }

for file in "$@"; do
  [[ -s "$file" ]] || { echo "missing or empty Android ELF: $file" >&2; exit 1; }
  header=$($reader -hW "$file")
  programs=$($reader -lW "$file")
  dynamic=$($reader -dW "$file")

  grep -Eq 'Type:[[:space:]]+DYN .*Position-Independent Executable|Type:[[:space:]]+DYN ' <<<"$header" || {
    echo "$file is not ET_DYN/PIE" >&2
    exit 1
  }
  grep -Eq 'Machine:[[:space:]]+AArch64' <<<"$header" || {
    echo "$file is not AArch64" >&2
    exit 1
  }
  grep -Fq '/system/bin/linker64' <<<"$programs" || {
    echo "$file does not request Android linker64 (generic Linux/static payload?)" >&2
    exit 1
  }
  if grep -Eq '\(TEXTREL\)|\(RPATH\)|\(RUNPATH\)' <<<"$dynamic"; then
    echo "$file contains TEXTREL/RPATH/RUNPATH" >&2
    exit 1
  fi
  if grep -Fq 'libc++_shared.so' <<<"$dynamic"; then
    echo "$file needs libc++_shared.so but the APK intentionally ships a static C++ runtime" >&2
    exit 1
  fi
  printf 'Android ELF OK: %s\n' "$file"
  $reader -hW "$file" | grep -E 'Class:|Data:|Type:|Machine:'
  $reader -lW "$file" | grep 'Requesting program interpreter'
  $reader -dW "$file" | grep '(NEEDED)' || true
done
