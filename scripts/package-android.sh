#!/usr/bin/env bash
# Assemble and sign an APK using stock Android SDK command line tools (no Gradle).
set -euo pipefail
cd "$(dirname "$0")/.."

: "${RPC_BIN:?ARM64 Android ggml-rpc-server required}"
: "${SERVER_BIN:?ARM64 Android llama-server required}"
: "${KEYSTORE:?Provide a private signing keystore outside this repository}"
: "${KS_PASS:?Set keystore password}"
: "${ANDROID_HOME:?Set ANDROID_HOME}"
export KS_PASS
version=0.1.2
bt=${BUILD_TOOLS:-$ANDROID_HOME/build-tools/34.0.0}
jar=${ANDROID_JAR:-$ANDROID_HOME/platforms/android-34/android.jar}
for tool in aapt d8 zipalign apksigner; do
  [[ -x "$bt/$tool" ]] || { echo "Missing Android build tool: $bt/$tool" >&2; exit 1; }
done
[[ -f "$jar" ]] || { echo "Missing Android platform jar: $jar" >&2; exit 1; }

"$PWD/scripts/verify-android-elf.sh" "$RPC_BIN" "$SERVER_BIN"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work"/{classes,dex,lib/arm64-v8a,assets/licenses} packages
source_commit=$(git rev-parse HEAD)

GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build \
  -buildmode=pie -buildvcs=false -trimpath \
  -ldflags="-s -w -X main.buildVersion=$version -X main.buildCommit=$source_commit" \
  -o "$work/lib/arm64-v8a/libylm_host.so" ./host
install -m 0755 "$RPC_BIN" "$work/lib/arm64-v8a/libylm_rpc.so"
install -m 0755 "$SERVER_BIN" "$work/lib/arm64-v8a/libylm_server.so"
"$PWD/scripts/verify-android-elf.sh" "$work/lib/arm64-v8a/"*.so

cp licenses/* "$work/assets/licenses/"
cat > "$work/assets/BUILDINFO.txt" <<EOF
YourLM: $version
Source commit: $source_commit
llama.cpp: b12562 / c0b97be0b297177a88df08e96c01420976bb18d0
Android ABI: arm64-v8a
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
  --out packages/YourLM-Android-arm64.apk "$work/aligned.apk"
"$bt/apksigner" verify --verbose --print-certs packages/YourLM-Android-arm64.apk

mkdir "$work/audit"
unzip -q packages/YourLM-Android-arm64.apk 'lib/arm64-v8a/*.so' -d "$work/audit"
"$PWD/scripts/verify-android-elf.sh" "$work/audit/lib/arm64-v8a/"*.so
sha256sum packages/YourLM-Android-arm64.apk
