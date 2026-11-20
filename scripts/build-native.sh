#!/usr/bin/env bash
# Build the Android executables with the official pinned NDK. Generic Linux or
# musl cross-compilers are intentionally rejected: Android 9+ requires PIE and
# the Android bionic ABI.
set -euo pipefail

: "${LLAMA_SRC:?Checkout ggml-org/llama.cpp at b12562}"
src=$(cd "$LLAMA_SRC" && pwd)
expected_llama=c0b97be0b297177a88df08e96c01420976bb18d0
[[ $(git -C "$src" rev-parse HEAD) == "$expected_llama" ]] || {
  echo "Expected llama.cpp b12562 ($expected_llama)" >&2
  exit 1
}

ndk=${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-${NDK_HOME:-}}}
: "${ndk:?Set ANDROID_NDK_HOME to Android NDK 27.2.12479018}"
ndk=$(cd "$ndk" && pwd)
expected_ndk=${YOURLM_NDK_VERSION:-27.2.12479018}
actual_ndk=$(sed -n 's/^Pkg.Revision[[:space:]]*=[[:space:]]*//p' "$ndk/source.properties" | head -1)
[[ "$actual_ndk" == "$expected_ndk" ]] || {
  echo "Expected Android NDK $expected_ndk, found ${actual_ndk:-unknown}" >&2
  exit 1
}

toolchain="$ndk/build/cmake/android.toolchain.cmake"
[[ -f "$toolchain" ]] || { echo "Missing NDK CMake toolchain: $toolchain" >&2; exit 1; }
abi=${ANDROID_ABI:-arm64-v8a}
platform=${ANDROID_PLATFORM:-28}
[[ "$abi" == arm64-v8a || "$abi" == x86_64 ]] || {
  echo "Supported build ABIs: arm64-v8a, x86_64" >&2
  exit 1
}

build=${1:-build/android-native}
mkdir -p "$build"
build=$(cd "$build" && pwd)

cmake -S "$src" -B "$build" \
  -DCMAKE_TOOLCHAIN_FILE="$toolchain" \
  -DANDROID_ABI="$abi" \
  -DANDROID_PLATFORM="android-$platform" \
  -DANDROID_STL=c++_static \
  -DCMAKE_BUILD_TYPE=MinSizeRel \
  -DCMAKE_POSITION_INDEPENDENT_CODE=ON \
  -DCMAKE_SKIP_RPATH=ON \
  -DGGML_NATIVE=OFF \
  -DGGML_RPC=ON \
  -DGGML_OPENMP=OFF \
  -DGGML_CPU_REPACK=OFF \
  -DGGML_BACKEND_DL=OFF \
  -DBUILD_SHARED_LIBS=OFF \
  -DLLAMA_OPENSSL=OFF \
  -DLLAMA_BUILD_TESTS=OFF \
  -DLLAMA_BUILD_EXAMPLES=OFF \
  -DLLAMA_BUILD_SERVER=ON \
  -DLLAMA_USE_PREBUILT_UI=OFF \
  -DLLAMA_BUILD_UI=OFF
cmake --build "$build" --target ggml-rpc-server llama-server --parallel "${JOBS:-2}"

rpc="$build/bin/ggml-rpc-server"
server="$build/bin/llama-server"
if [[ "$abi" == arm64-v8a ]]; then
  READELF="$ndk/toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-readelf" \
    "$(dirname "$0")/verify-android-elf.sh" "$rpc" "$server"
else
  reader="$ndk/toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-readelf"
  for file in "$rpc" "$server"; do
    "$reader" -hW "$file" | grep -Eq 'Type:[[:space:]]+DYN '
    "$reader" -hW "$file" | grep -Eq 'Machine:[[:space:]]+(Advanced Micro Devices X86-64|x86-64)'
    "$reader" -lW "$file" | grep -Fq '/system/bin/linker64'
    echo "Android x86_64 PIE OK: $file"
  done
fi
sha256sum "$rpc" "$server"
