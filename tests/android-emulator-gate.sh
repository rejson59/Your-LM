#!/usr/bin/env bash
# Run as one command by android-emulator-runner. That action intentionally
# executes each YAML script line in a separate `sh -c`, so all stateful and
# multi-line logic belongs in this helper rather than in the workflow block.
set -euo pipefail

apk=${1:?Pass the x86_64 emulator APK}
adb push build/android-x86-native/bin/ggml-rpc-server /data/local/tmp/ggml-rpc-server
adb push build/android-x86-native/bin/llama-server /data/local/tmp/llama-server
adb shell chmod 700 /data/local/tmp/ggml-rpc-server /data/local/tmp/llama-server

python3 - <<'PY'
import subprocess

checks = [
    (["adb", "shell", "/data/local/tmp/ggml-rpc-server", "--help"], "/tmp/rpc-help.txt"),
    (["adb", "shell", "/data/local/tmp/llama-server", "--version"], "/tmp/server-version.txt"),
]
for command, path in checks:
    with open(path, "w", encoding="utf-8") as output:
        subprocess.run(
            command,
            stdout=output,
            stderr=subprocess.STDOUT,
            timeout=45,
            check=True,
        )
PY

grep -q 'Usage:' /tmp/rpc-help.txt
grep -q 'version:' /tmp/server-version.txt

report_failure() {
  local title=$1 log=$2
  local result
  result=$(tail -80 "$log")
  result=${result//'%'/'%25'}
  result=${result//$'\n'/'%0A'}
  result=${result//$'\r'/'%0D'}
  echo "::error title=$title::$result"
}

if ! /bin/bash tests/android-engine-smoke.sh 2>&1 | tee "$RUNNER_TEMP/android-engine-smoke.log"; then
  report_failure 'Android inference smoke failed' "$RUNNER_TEMP/android-engine-smoke.log"
  exit 1
fi
if ! /bin/bash tests/android-smoke.sh "$apk" 2>&1 | tee "$RUNNER_TEMP/android-ui-smoke.log"; then
  report_failure 'Android UI smoke failed' "$RUNNER_TEMP/android-ui-smoke.log"
  exit 1
fi
