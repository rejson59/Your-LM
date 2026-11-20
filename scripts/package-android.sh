#!/usr/bin/env bash
# Assemble and sign an APK using stock Android SDK command line tools (no Gradle).
set -euo pipefail
cd "$(dirname "$0")/.."
: "${RPC_BIN:?ARM64 ggml-rpc-server required}" "${SERVER_BIN:?ARM64 llama-server required}" "${KEYSTORE:?Provide a private signing keystore outside this repository}" "${KS_PASS:?Set keystore password}"; export KS_PASS
bt=${BUILD_TOOLS:-${ANDROID_HOME:?}/build-tools/34.0.0}; jar=${ANDROID_JAR:-$ANDROID_HOME/platforms/android-34/android.jar}
w=$(mktemp -d); trap 'rm -rf "$w"' EXIT
mkdir -p "$w"/{classes,dex,lib/arm64-v8a,assets/licenses} packages
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$w/lib/arm64-v8a/libylm_host.so" ./host
cp "$RPC_BIN" "$w/lib/arm64-v8a/libylm_rpc.so"; cp "$SERVER_BIN" "$w/lib/arm64-v8a/libylm_server.so"; cp licenses/* "$w/assets/licenses/"
find android/src android/vendor -name '*.java' > "$w/sources"
javac -encoding UTF-8 -source 8 -target 8 -bootclasspath "$jar:$bt/core-lambda-stubs.jar" -d "$w/classes" @"$w/sources"
(cd "$w/classes" && jar cf "$w/app.jar" .)
"$bt/d8" --min-api 28 --lib "$jar" --output "$w/dex" "$w/app.jar"
"$bt/aapt" package -f -M android/AndroidManifest.xml -S android/res -I "$jar" -F "$w/node.apk"
(cd "$w" && cp dex/classes.dex . && zip -q -r node.apk classes.dex lib assets)
"$bt/zipalign" -f 4 "$w/node.apk" "$w/aligned.apk"
"$bt/apksigner" sign --v4-signing-enabled false --ks "$KEYSTORE" --ks-pass env:KS_PASS --ks-key-alias "${KS_ALIAS:-yourlm}" --out packages/YourLM-Android-arm64.apk "$w/aligned.apk"
"$bt/apksigner" verify --verbose --print-certs packages/YourLM-Android-arm64.apk
