#!/usr/bin/env bash
# Build the isolated Android ARM64 llama-server used by YourLM's balanced MoE
# profile. The stock b12562 server and RPC worker remain separate and unchanged.
set -euo pipefail
cd "$(dirname "$0")/.."

: "${SMOL_LLAMA_SRC:?Checkout ggml-org/llama.cpp at v0.8.0 commit 8642541}"
SMOL_LLAMA_SRC=$(cd "$SMOL_LLAMA_SRC" && pwd)
export SMOL_LLAMA_SRC
scripts/apply-smolmaxxing-patch.sh

ndk=${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-${NDK_HOME:-}}}
: "${ndk:?Set ANDROID_NDK_HOME to Android NDK 27.2.12479018}"
ndk=$(cd "$ndk" && pwd)
expected_ndk=${YOURLM_NDK_VERSION:-27.2.12479018}
actual_ndk=$(sed -n 's/^Pkg.Revision[[:space:]]*=[[:space:]]*//p' "$ndk/source.properties" | head -1)
[[ "$actual_ndk" == "$expected_ndk" ]] || {
  echo "Expected Android NDK $expected_ndk, found ${actual_ndk:-unknown}" >&2
  exit 1
}
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) ndk_host=linux-x86_64 ;;
  Darwin-x86_64) ndk_host=darwin-x86_64 ;;
  *) echo "Unsupported NDK build host: $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac
ndk_bin="$ndk/toolchains/llvm/prebuilt/$ndk_host/bin"
toolchain="$ndk/build/cmake/android.toolchain.cmake"
[[ -f "$toolchain" ]] || { echo "Missing NDK CMake toolchain: $toolchain" >&2; exit 1; }

# The measured Q2_K kernels require ARMv8.2 dot-product + fp16. YourLM checks
# /proc/cpuinfo before selecting this binary and falls back to stock otherwise.
build=${1:-build/android-smol-native}
mkdir -p "$build"
build=$(cd "$build" && pwd)
cmake -S "$SMOL_LLAMA_SRC" -B "$build" \
  -DCMAKE_TOOLCHAIN_FILE="$toolchain" \
  -DANDROID_ABI=arm64-v8a \
  -DANDROID_PLATFORM=android-28 \
  -DANDROID_STL=c++_static \
  -DCMAKE_BUILD_TYPE=MinSizeRel \
  -DCMAKE_POSITION_INDEPENDENT_CODE=ON \
  -DCMAKE_SKIP_RPATH=ON \
  -DGGML_NATIVE=OFF \
  -DGGML_CPU_ARM_ARCH=armv8.2-a+dotprod+fp16 \
  -DGGML_CPU_REPACK=ON \
  -DGGML_RPC=OFF \
  -DGGML_OPENMP=OFF \
  -DGGML_BACKEND_DL=OFF \
  -DBUILD_SHARED_LIBS=OFF \
  -DLLAMA_OPENSSL=OFF \
  -DLLAMA_BUILD_TESTS=OFF \
  -DLLAMA_BUILD_EXAMPLES=OFF \
  -DLLAMA_BUILD_SERVER=ON \
  -DLLAMA_USE_PREBUILT_UI=OFF \
  -DLLAMA_BUILD_UI=OFF
cmake --build "$build" --target llama-server --parallel "${JOBS:-2}"

server="$build/bin/llama-server"
strip="$ndk_bin/llvm-strip"
[[ -x "$strip" ]] || { echo "Missing NDK llvm-strip: $strip" >&2; exit 1; }
"$strip" --strip-unneeded "$server"
ANDROID_ABI=arm64-v8a READELF="$ndk_bin/llvm-readelf" scripts/verify-android-elf.sh "$server"
sha256sum "$server"
