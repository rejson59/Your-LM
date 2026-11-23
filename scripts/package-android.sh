#!/usr/bin/env bash
# Assemble and sign an APK using stock Android SDK command line tools (no Gradle).
set -euo pipefail
cd "$(dirname "$0")/.."

: "${RPC_BIN:?Android ggml-rpc-server required}"
: "${SERVER_BIN:?Android llama-server required}"
: "${KEYSTORE:?Provide a private signing keystore outside this repository}"
: "${KS_PASS:?Set keystore password}"
: "${ANDROID_HOME:?Set ANDROID_HOME}"
export KS_PASS
version=0.2.0-rc16
abi=${ANDROID_ABI:-arm64-v8a}
case "$abi" in
  arm64-v8a)
    goarch=arm64
    default_apk=packages/YourLM-Android-arm64.apk
    ;;
  x86_64)
    goarch=amd64
    default_apk=packages/YourLM-Android-x86_64-test.apk
    ;;
  *)
    echo "Supported package ABIs: arm64-v8a, x86_64" >&2
    exit 1
    ;;
esac
apk_out=${APK_OUT:-$default_apk}
bt=${BUILD_TOOLS:-$ANDROID_HOME/build-tools/34.0.0}
jar=${ANDROID_JAR:-$ANDROID_HOME/platforms/android-34/android.jar}
for tool in aapt d8 zipalign apksigner; do
  [[ -x "$bt/$tool" ]] || { echo "Missing Android build tool: $bt/$tool" >&2; exit 1; }
done
[[ -f "$jar" ]] || { echo "Missing Android platform jar: $jar" >&2; exit 1; }

native_inputs=("$RPC_BIN" "$SERVER_BIN")
if [[ "$abi" == arm64-v8a ]]; then
  : "${SMOL_SERVER_BIN:?Android smolmaxxing llama-server required for arm64 release}"
  native_inputs+=("$SMOL_SERVER_BIN")
fi
ANDROID_ABI="$abi" "$PWD/scripts/verify-android-elf.sh" "${native_inputs[@]}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/classes" "$work/dex" "$work/lib/$abi" "$work/assets/licenses" "$(dirname "$apk_out")"
source_commit=$(git rev-parse HEAD)

if [[ "$abi" == x86_64 ]]; then
  : "${ANDROID_NDK_HOME:?x86_64 emulator packaging requires ANDROID_NDK_HOME}"
  case "$(uname -s)-$(uname -m)" in
    Linux-x86_64) ndk_host=linux-x86_64 ;;
    Darwin-x86_64) ndk_host=darwin-x86_64 ;;
    *) echo "Unsupported x86_64 packaging host: $(uname -s)-$(uname -m)" >&2; exit 1 ;;
  esac
  android_cc="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/$ndk_host/bin/x86_64-linux-android28-clang"
  [[ -x "$android_cc" ]] || { echo "Missing Android x86_64 clang: $android_cc" >&2; exit 1; }
  go_env=(GOOS=android GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=1 CC="$android_cc")
else
  go_env=(GOOS=android GOARCH="$goarch" CGO_ENABLED=0)
fi
env "${go_env[@]}" go build \
  -buildmode=pie -buildvcs=false -trimpath \
  -ldflags="-s -w -X main.buildVersion=$version -X main.buildCommit=$source_commit" \
  -o "$work/lib/$abi/libylm_host.so" ./host
install -m 0755 "$RPC_BIN" "$work/lib/$abi/libylm_rpc.so"
install -m 0755 "$SERVER_BIN" "$work/lib/$abi/libylm_server.so"
if [[ "$abi" == arm64-v8a ]]; then
  install -m 0755 "$SMOL_SERVER_BIN" "$work/lib/$abi/libylm_smol.so"
fi
ANDROID_ABI="$abi" "$PWD/scripts/verify-android-elf.sh" "$work/lib/$abi/"*.so

cp licenses/* "$work/assets/licenses/"
cat > "$work/assets/BUILDINFO.txt" <<EOF
YourLM: $version
Source commit: $source_commit
llama.cpp: b12562 / c0b97be0b297177a88df08e96c01420976bb18d0
YourLM native patch: bounded streaming RPC cache v1
RPC compatibility: b12562-ylm-rpc5
smolmaxxing runtime: commit 8d12bb17fd389c25f3c6167a5b73f93034d6324f patch SHA-256 f0677c48522e1659969998654d83db9ede7652e771e045dbcd35427abd0230bd
smolmaxxing llama.cpp base: v0.8.0 / 8642541b255bfb5dc39cd351a178be09013a55d6 (arm64 release only)
Android ABI: $abi
Minimum Android API: 28
Native toolchain: Android NDK 27.2.12479018
EOF

find android/src android/vendor -name '*.java' -print | sort > "$work/sources"
javac -encoding UTF-8 -source 8 -target 8 \
  -bootclasspath "$jar:$bt/core-lambda-stubs.jar" \
  -d "$work/classes" @"$work/sources"
(cd "$work/classes" && jar cf "$work/app.jar" .)
"$bt/d8" --min-api 28 --lib "$jar" --output "$work/dex" "$work/app.jar"
"$bt/aapt" package -f -M android/AndroidManifest.xml -S android/res -I "$jar" -F "$work/unsigned.apk"
(
  cd "$work"
  cp dex/classes.dex .
  zip -q -r unsigned.apk classes.dex lib assets
)
"$bt/zipalign" -f 4 "$work/unsigned.apk" "$work/aligned.apk"
"$bt/apksigner" sign \
  --v4-signing-enabled false \
  --ks "$KEYSTORE" \
  --ks-pass env:KS_PASS \
  --ks-key-alias "${KS_ALIAS:-yourlm}" \
  --out "$apk_out" "$work/aligned.apk"
"$bt/apksigner" verify --verbose --print-certs "$apk_out"
cert_digest=$("$bt/apksigner" verify --print-certs "$apk_out" | sed -n 's/^Signer #1 certificate SHA-256 digest: //p')
if [[ -n "${EXPECTED_ANDROID_CERT_SHA256:-}" && "$cert_digest" != "$EXPECTED_ANDROID_CERT_SHA256" ]]; then
  echo "Android signing certificate does not match the stable update identity" >&2
  exit 1
fi
printf 'Android signing certificate SHA-256: %s\n' "$cert_digest"

mkdir "$work/audit"
unzip -q "$apk_out" "lib/$abi/*.so" -d "$work/audit"
ANDROID_ABI="$abi" "$PWD/scripts/verify-android-elf.sh" "$work/audit/lib/$abi/"*.so
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$apk_out"
else
  shasum -a 256 "$apk_out"
fi
