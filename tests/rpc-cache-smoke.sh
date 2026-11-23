#!/usr/bin/env bash
# Build the patched host RPC server and prove cache miss, hit, and recovery from
# incomplete/corrupt files without loading a real model.
set -euo pipefail

: "${LLAMA_SRC:?Set LLAMA_SRC to the pinned llama.cpp checkout}"
repo=$(cd "$(dirname "$0")/.." && pwd)
src=$(cd "$LLAMA_SRC" && pwd)
build=${1:-"${RUNNER_TEMP:-/tmp}/rpc-cache-smoke"}
rm -rf "$build"
mkdir -p "$build"
build=$(cd "$build" && pwd)

LLAMA_SRC="$src" "$repo/scripts/apply-llama-patches.sh"
cmake -S "$src" -B "$build/native" \
  -DCMAKE_BUILD_TYPE=Release \
  -DGGML_NATIVE=OFF \
  -DGGML_RPC=ON \
  -DGGML_OPENMP=OFF \
  -DGGML_CPU_REPACK=OFF \
  -DGGML_BACKEND_DL=OFF \
  -DBUILD_SHARED_LIBS=OFF \
  -DLLAMA_OPENSSL=OFF \
  -DLLAMA_BUILD_TESTS=OFF \
  -DLLAMA_BUILD_EXAMPLES=OFF \
  -DLLAMA_BUILD_SERVER=OFF \
  -DLLAMA_BUILD_UI=OFF \
  -DLLAMA_USE_PREBUILT_UI=OFF
cmake --build "$build/native" --target ggml-rpc-server --parallel "${JOBS:-2}"

"${CXX:-c++}" -std=c++17 -O2 "$repo/tests/rpc-cache-smoke.cpp" \
  -I"$src/ggml/include" -I"$src/ggml/src/ggml-rpc" \
  "$build/native/ggml/src/ggml-rpc/libggml-rpc.a" \
  "$build/native/ggml/src/libggml.a" \
  "$build/native/ggml/src/libggml-cpu.a" \
  "$build/native/ggml/src/libggml-base.a" \
  -pthread -ldl -o "$build/cache-client"

port=$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    print(sock.getsockname()[1])
PY
)
cache="$build/cache"
mkdir -p "$cache"
LLAMA_CACHE="$cache" GGML_RPC_LOG_LEVEL=2 \
  "$build/native/bin/ggml-rpc-server" -H 127.0.0.1 -p "$port" -c >"$build/server.log" 2>&1 &
server_pid=$!
cleanup() { kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; }
trap cleanup EXIT
for _ in $(seq 1 50); do
  if (echo >/dev/tcp/127.0.0.1/"$port") 2>/dev/null; then break; fi
  sleep .1
done
kill -0 "$server_pid"
endpoint="127.0.0.1:$port"

# Initial miss writes the file; the second transfer must hit it.
"$build/cache-client" "$endpoint"
"$build/cache-client" "$endpoint"
cache_file=$(find "$cache/rpc" -maxdepth 1 -type f ! -name '.incoming-*.part' | head -1)
[[ -n "$cache_file" && $(stat -c %s "$cache_file") == 12582912 ]]

# A short file and a same-sized bit flip must both be rejected and rebuilt.
truncate -s 1048576 "$cache_file"
"$build/cache-client" "$endpoint"
python3 - "$cache_file" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
data = bytearray(path.read_bytes())
data[len(data)//2] ^= 0xff
path.write_bytes(data)
PY
"$build/cache-client" "$endpoint"
[[ $(stat -c %s "$cache_file") == 12582912 ]]

cleanup
trap - EXIT
grep -Fq "removing incomplete cache file" "$build/server.log"
grep -Fq "removing corrupt cache file" "$build/server.log"
[[ $(grep -Fc "saved to" "$build/server.log") -eq 3 ]]
! find "$cache" -type f -name '.incoming-*.part' | grep -q .
echo "Bounded RPC cache miss/hit/recovery smoke test passed"
