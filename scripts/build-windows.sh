#!/usr/bin/env bash
# Reproducible, unpacked Windows x64 package. It does not create an installer
# and the controller contains no downloader. Requires Go, CMake, Ninja and Zig.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${LLAMA_SRC:?Checkout ggml-org/llama.cpp at the pinned commit}"
src=$(cd "$LLAMA_SRC" && pwd)
commit=c0b97be0b297177a88df08e96c01420976bb18d0
if [[ $(git -C "$src" rev-parse HEAD) != "$commit" ]]; then
  echo "Expected llama.cpp b12562 ($commit)" >&2
  exit 1
fi
for tool in go cmake ninja zig; do command -v "$tool" >/dev/null || { echo "Missing $tool" >&2; exit 1; }; done

build=${WINDOWS_BUILD_DIR:-build/windows-native}
rm -rf "$build"
mkdir -p "$build/toolchain"
build=$(cd "$build" && pwd)
z=$(command -v zig)
for tool in ar ranlib; do
  printf '#!/bin/sh\nexec "%s" %s "$@"\n' "$z" "$tool" >"$build/toolchain/$tool"
  chmod +x "$build/toolchain/$tool"
done
# Zig provides an rc.exe-compatible resource compiler, while CMake invokes a
# GNU-style windres command. The final two arguments are input and output.
cat >"$build/toolchain/windres" <<EOF
#!/bin/sh
while [ "\$#" -gt 2 ]; do shift; done
exec "$z" rc /fo "\$2" "\$1"
EOF
chmod +x "$build/toolchain/windres"
export PATH="$build/toolchain:$PATH"
cat >"$build/toolchain/windows.cmake" <<EOF
set(CMAKE_SYSTEM_NAME Windows)
set(CMAKE_SYSTEM_PROCESSOR AMD64)
set(CMAKE_C_COMPILER "$z" cc -target x86_64-windows-gnu)
set(CMAKE_CXX_COMPILER "$z" c++ -target x86_64-windows-gnu)
set(CMAKE_AR "$build/toolchain/ar")
set(CMAKE_RANLIB "$build/toolchain/ranlib")
set(CMAKE_EXE_LINKER_FLAGS "-static -s")
EOF
prefix="-ffile-prefix-map=$src=llama.cpp -ffile-prefix-map=$build=build"
cmake -S "$src" -B "$build/llama" -G Ninja \
  -DCMAKE_TOOLCHAIN_FILE="$build/toolchain/windows.cmake" \
  -DCMAKE_C_FLAGS="$prefix" -DCMAKE_CXX_FLAGS="$prefix" \
  -DCMAKE_BUILD_TYPE=Release -DGGML_NATIVE=OFF -DGGML_RPC=ON \
  -DGGML_CCACHE=OFF -DBUILD_SHARED_LIBS=OFF -DGGML_OPENMP=OFF \
  -DGGML_CPU_REPACK=OFF -DLLAMA_OPENSSL=OFF -DLLAMA_SUBPROCESS=OFF \
  -DMTMD_VIDEO=OFF -DLLAMA_BUILD_TESTS=OFF \
  -DLLAMA_BUILD_COMMIT="$commit" -DLLAMA_BUILD_NUMBER=12562 \
  -DLLAMA_BUILD_IS_DEV=OFF -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_SERVER=ON \
  -DLLAMA_USE_PREBUILT_UI=OFF -DLLAMA_BUILD_UI=OFF
cmake --build "$build/llama" --target ggml-rpc-server llama-server --parallel "${JOBS:-2}"

out=packages/YourLM-Windows-x64
rm -rf "$out"
mkdir -p "$out/runtime"
cp "$build/llama/bin/llama-server.exe" "$out/runtime/"
cp "$build/llama/bin/ggml-rpc-server.exe" "$out/runtime/"
server=$(sha256sum "$out/runtime/llama-server.exe" | cut -d' ' -f1)
rpc=$(sha256sum "$out/runtime/ggml-rpc-server.exe" | cut -d' ' -f1)
source_commit=$(git rev-parse HEAD)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=false \
  -ldflags="-X main.serverHash=$server -X main.rpcHash=$rpc -X main.buildVersion=0.1.2 -X main.buildCommit=$source_commit" \
  -o "$out/YourLM.exe" ./host
cp windows/README-Windows.txt "$out/CZYTAJ-MNIE.txt"
cat >"$out/BUILDINFO.txt" <<EOF
YourLM: 0.1.2
YourLM source: $source_commit
llama.cpp source: $commit (b12562)
Go: $(go version)
Zig: $(zig version)
llama-server.exe SHA-256: $server
ggml-rpc-server.exe SHA-256: $rpc
EOF
(
  cd "$out"
  sha256sum BUILDINFO.txt CZYTAJ-MNIE.txt YourLM.exe runtime/llama-server.exe runtime/ggml-rpc-server.exe >SHA256SUMS.txt
)
printf 'Windows package created in %s\n' "$out"
