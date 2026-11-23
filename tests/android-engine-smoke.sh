#!/usr/bin/env bash
# Exercise model loading and token generation in the Android emulator build of
# llama-server, using a tiny synthetic GGUF (plumbing only, not language quality).
set -euo pipefail
python3 tests/tiny_model.py /tmp/yourlm-android-tiny.gguf
adb push /tmp/yourlm-android-tiny.gguf /data/local/tmp/yourlm-tiny.gguf >/dev/null
adb forward tcp:18080 tcp:18080 >/dev/null

rpc_pid=$(adb shell "sh -c '/data/local/tmp/ggml-rpc-server -H 127.0.0.1 -p 50052 -t 1 >/data/local/tmp/yourlm-rpc.log 2>&1 & echo \$!'" | tr -d '\r')
sleep 1
pid=$(adb shell "sh -c '/data/local/tmp/llama-server --model /data/local/tmp/yourlm-tiny.gguf --offline --no-webui --host 127.0.0.1 --port 18080 --api-key android-gate --ctx-size 64 --parallel 1 -t 1 --rpc 127.0.0.1:50052 --split-mode layer --fit on --fit-target 128 >/data/local/tmp/yourlm-server.log 2>&1 & echo \$!'" | tr -d '\r')
cleanup() {
  adb shell kill "$pid" "$rpc_pid" >/dev/null 2>&1 || true
  adb forward --remove tcp:18080 >/dev/null 2>&1 || true
}
trap cleanup EXIT

ready=false
attempt=0
while ((attempt < 120)); do
  if curl --noproxy '*' -fsS -H 'Authorization: Bearer android-gate' http://127.0.0.1:18080/v1/models >/tmp/android-models.json 2>/dev/null; then
    ready=true
    break
  fi
  attempt=$((attempt + 1))
  sleep 1
done
if [[ "$ready" != true ]]; then
  adb shell cat /data/local/tmp/yourlm-rpc.log || true
  adb shell cat /data/local/tmp/yourlm-server.log || true
  echo "Android llama-server did not become ready with RPC --fit" >&2
  exit 1
fi
curl --noproxy '*' -fsS \
  -H 'Authorization: Bearer android-gate' -H 'Content-Type: application/json' \
  -d '{"prompt":"test","n_predict":2,"temperature":0}' \
  http://127.0.0.1:18080/completion >/tmp/android-completion.json
python3 - <<'PY'
import json
models=json.load(open('/tmp/android-models.json'))
reply=json.load(open('/tmp/android-completion.json'))
assert models.get('data'), models
assert isinstance(reply.get('content'), str), reply
print('Android emulator synthetic GGUF inference OK:', reply.get('tokens_predicted'), 'tokens')
PY
