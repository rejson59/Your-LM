#!/usr/bin/env bash
# Reproducible single-file Windows x64 build. llama.cpp, the Android APK and
# the minimal Windows ADB payload are compressed into YourLM.exe and expanded
# only under the user's private application-data directory at runtime.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${LLAMA_SRC:?Checkout ggml-org/llama.cpp at the pinned commit}"
src=$(cd "$LLAMA_SRC" && pwd)
commit=c0b97be0b297177a88df08e96c01420976bb18d0
version=0.2.0-rc13
if [[ $(git -C "$src" rev-parse HEAD) != "$commit" ]]; then
  echo "Expected llama.cpp b12562 ($commit)" >&2
  exit 1
fi
LLAMA_SRC="$src" scripts/apply-llama-patches.sh
for tool in go cmake ninja zig python3; do
  command -v "$tool" >/dev/null || { echo "Missing $tool" >&2; exit 1; }
done
adb_bundle=${WINDOWS_ADB_BUNDLE:-windows/platform-tools-windows.zip}
adb_bundle_sha256=63b73f6f1870305c5a3e0f43ca861427fe6856cc113392710c7e698ab477e3f9
apk=${ANDROID_APK:-packages/YourLM-Android-arm64.apk}
[[ -s "$adb_bundle" ]] || { echo "Missing Windows ADB bundle: $adb_bundle" >&2; exit 1; }
[[ -s "$apk" ]] || { echo "Missing Android APK to embed: $apk" >&2; exit 1; }
if [[ ${ALLOW_UNPINNED_ADB:-0} != 1 && $(sha256sum "$adb_bundle" | cut -d' ' -f1) != "$adb_bundle_sha256" ]]; then
  echo "Windows ADB bundle does not match the pinned payload" >&2
  exit 1
fi

build=${WINDOWS_BUILD_DIR:-build/windows-native}
rm -rf "$build"
mkdir -p "$build/toolchain"
build=$(cd "$build" && pwd)
z=$(command -v zig)
for tool in ar ranlib; do
  printf '#!/bin/sh\nexec "%s" %s "$@"\n' "$z" "$tool" >"$build/toolchain/$tool"
  chmod +x "$build/toolchain/$tool"
done
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

server_file="$build/llama/bin/llama-server.exe"
rpc_file="$build/llama/bin/ggml-rpc-server.exe"
server=$(sha256sum "$server_file" | cut -d' ' -f1)
rpc=$(sha256sum "$rpc_file" | cut -d' ' -f1)
source_commit=$(git rev-parse HEAD)
embedded=host/embedded
rm -rf "$embedded"
mkdir -p "$embedded"
trap 'rm -rf host/embedded' EXIT
python3 - "$server_file" "$rpc_file" "$embedded/runtime.zip" <<'PY'
from pathlib import Path
from sys import argv
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo

def add(out, source):
    info=ZipInfo(Path(source).name,(2026,1,1,0,0,0))
    info.compress_type=ZIP_DEFLATED
    info.external_attr=0o700 << 16
    out.writestr(info,Path(source).read_bytes())
with ZipFile(argv[3],'w',compresslevel=9) as out:
    add(out,argv[1]); add(out,argv[2])
PY
cp "$adb_bundle" "$embedded/platform-tools.zip"
cp "$apk" "$embedded/YourLM-Android-arm64.apk"
python3 - "$embedded/platform-tools.zip" <<'PY'
from sys import argv
from zipfile import ZipFile
with ZipFile(argv[1]) as z:
    names={n.rsplit('/',1)[-1].lower() for n in z.namelist() if not n.endswith('/')}
required={'adb.exe','adbwinapi.dll','adbwinusbapi.dll'}
missing=required-names
if missing: raise SystemExit('ADB bundle is incomplete: '+', '.join(sorted(missing)))
PY
mkdir -p packages
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=false \
  -ldflags="-H=windowsgui -X main.serverHash=$server -X main.rpcHash=$rpc -X main.buildVersion=$version -X main.buildCommit=$source_commit" \
  -o packages/YourLM.exe ./host
# The Windows payload is embedded above; keep the separately published APK exact.
[[ $(sha256sum "$apk" | cut -d' ' -f1) == $(sha256sum "$embedded/YourLM-Android-arm64.apk" | cut -d' ' -f1) ]]
(cd packages && sha256sum YourLM.exe YourLM-Android-arm64.apk > SHA256SUMS)
printf 'Single-file Windows build created: packages/YourLM.exe\n'
sha256sum packages/YourLM.exe
