#!/usr/bin/env bash
# Runs inside the GitHub x86_64 Android emulator gate. It launches an
# architecture-equivalent test APK, starts the real native worker through the
# UI, and checks manual pairing plus phone-host startup.
set -euo pipefail
apk=${1:-packages/YourLM-Android-arm64.apk}
xml=${RUNNER_TEMP:-/tmp}/yourlm-window.xml

adb install -r "$apk" >/dev/null
adb logcat -c
adb shell am force-stop pl.yourlm.node
adb shell am start -W -n pl.yourlm.node/.NodeActivity >/dev/null

pull_ui() {
  adb shell uiautomator dump /sdcard/yourlm-window.xml >/dev/null
  adb pull /sdcard/yourlm-window.xml "$xml" >/dev/null
}
assert_text() {
  pull_ui
  python3 - "$xml" "$1" <<'PY'
import sys, xml.etree.ElementTree as ET
texts=[n.attrib.get("text","") for n in ET.parse(sys.argv[1]).iter("node")]
if not any(sys.argv[2] in value for value in texts):
    raise SystemExit(f"missing UI text {sys.argv[2]!r}; visible={texts}")
PY
}
click_text() {
  local target=$1 point x y attempt=0
  local size screen_height
  size=$(adb shell wm size | tr -d '\r' | tail -1)
  screen_height=${size##*x}
  [[ "$screen_height" =~ ^[0-9]+$ ]] || screen_height=1920
  while ((attempt < 6)); do
    pull_ui
    point=$(python3 - "$xml" "$target" <<'PY'
import re, sys, xml.etree.ElementTree as ET
for node in ET.parse(sys.argv[1]).iter("node"):
    if sys.argv[2] in node.attrib.get("text", ""):
        x1,y1,x2,y2=map(int,re.findall(r"\d+",node.attrib["bounds"]))
        print((x1+x2)//2,(y1+y2)//2)
        break
PY
)
    if [[ -n "$point" ]]; then
      read -r x y <<<"$point"
      if ((y >= 80 && y <= screen_height - 120)); then
        adb shell input tap "$x" "$y"
        return
      fi
    fi
    adb shell input swipe 500 $((screen_height * 4 / 5)) 500 $((screen_height / 3)) 350
    sleep 1
    attempt=$((attempt + 1))
  done
  echo "cannot bring UI text into view: $target" >&2
  return 1
}

assert_text "Jak chcesz użyć tego urządzenia?"
assert_text "Telefon jako worker"
mkdir -p test-output
adb exec-out screencap -p > test-output/android-home.png
click_text "Telefon jako worker"
sleep 1
assert_text "Dodaj moc telefonu do laptopa"
click_text "Uruchom worker"

attempt=0
while ((attempt < 30)); do
  sleep 1
  pull_ui
  if grep -q 'WORKER GOTOWY' "$xml"; then break; fi
  attempt=$((attempt + 1))
done
python3 - "$xml" <<'PY'
import sys, xml.etree.ElementTree as ET
texts=[n.attrib.get("text","") for n in ET.parse(sys.argv[1]).iter("node")]
if not any("WORKER GOTOWY" in value for value in texts):
    raise SystemExit(f"worker did not become ready; visible={texts}")
PY
adb exec-out screencap -p > test-output/android-worker-ready.png
adb shell input swipe 500 1600 500 450 500
sleep 1
pull_ui
python3 - "$xml" <<'PY'
import re, sys, xml.etree.ElementTree as ET
texts=[n.attrib.get("text","") for n in ET.parse(sys.argv[1]).iter("node")]
if not any(re.fullmatch(r"(?:[A-HJ-NP-Z2-9]{5}-){3}[A-HJ-NP-Z2-9]{5}", value) for value in texts):
    raise SystemExit(f"manual 100-bit pairing key missing; visible={texts}")
if not any(re.fullmatch(r"(?:10|172\.(?:1[6-9]|2\d|3[01])|192\.168)\.[0-9.]+:\d+", value) for value in texts):
    raise SystemExit(f"private pairing endpoint missing; visible={texts}")
PY
cp "$xml" test-output/android-worker-ready.xml

if adb logcat -d '*:E' | grep -q 'FATAL EXCEPTION.*pl.yourlm.node'; then
  adb logcat -d
  echo "Android app crashed" >&2
  exit 1
fi
adb shell am force-stop pl.yourlm.node
adb shell am start -W -n pl.yourlm.node/.NodeActivity >/dev/null
assert_text "Model na telefonie"
click_text "Model na telefonie"
sleep 1
assert_text "Czat i API bez laptopa"
click_text "Otwórz konsolę"
attempt=0
while ((attempt < 20)); do
  sleep 1
  if adb shell ps -A | grep -q 'libylm_host'; then break; fi
  attempt=$((attempt + 1))
done
adb shell ps -A | grep 'libylm_host'
pull_ui
if grep -q 'Nie udało się uruchomić hosta' "$xml"; then
  cat "$xml"
  exit 1
fi
adb exec-out screencap -p > test-output/android-host.png
adb shell am force-stop pl.yourlm.node
