#!/usr/bin/env bash
# Build the exact native programs shipped in the APK. Zig supplies musl/libc++.
set -euo pipefail
: "${LLAMA_SRC:?Checkout ggml-org/llama.cpp at b12562}"; src=$(cd "$LLAMA_SRC" && pwd)
if [[ $(git -C "$src" rev-parse HEAD) != c0b97be0b297177a88df08e96c01420976bb18d0 ]]; then echo 'Expected llama.cpp b12562' >&2; exit 1; fi
b=${1:-build/android-native}; mkdir -p "$b"; b=$(cd "$b" && pwd)
z=$(command -v zig); for t in ar ranlib; do printf '#!/bin/sh\nexec "%s" %s "$@"\n' "$z" "$t" > "$b/$t"; chmod +x "$b/$t"; done
cat > "$b/arm.cmake" <<EOT
set(CMAKE_SYSTEM_NAME Linux)
set(CMAKE_SYSTEM_PROCESSOR aarch64)
set(CMAKE_C_COMPILER "$z" cc -target aarch64-linux-musl)
set(CMAKE_CXX_COMPILER "$z" c++ -target aarch64-linux-musl)
set(CMAKE_EXE_LINKER_FLAGS "-static")
set(CMAKE_AR "$b/ar")
set(CMAKE_RANLIB "$b/ranlib")
EOT
cmake -S "$src" -B "$b" -DCMAKE_TOOLCHAIN_FILE="$b/arm.cmake" -DCMAKE_BUILD_TYPE=MinSizeRel -DGGML_NATIVE=OFF -DGGML_RPC=ON -DBUILD_SHARED_LIBS=OFF -DGGML_OPENMP=OFF -DGGML_CPU_REPACK=OFF -DLLAMA_OPENSSL=OFF -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_SERVER=ON -DLLAMA_USE_PREBUILT_UI=OFF -DLLAMA_BUILD_UI=OFF
cmake --build "$b" --target ggml-rpc-server llama-server --parallel "${JOBS:-2}"
