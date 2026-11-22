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
  local wait_point attempt

  # A cold, software-rendered CI emulator can briefly report an ANR in Android's
  # own System UI. Choose “Wait” and re-dump the app; never dismiss an ANR from
  # YourLM itself, which must remain a test failure.
  for attempt in 1 2 3; do
    adb shell uiautomator dump /sdcard/yourlm-window.xml >/dev/null
    adb pull /sdcard/yourlm-window.xml "$xml" >/dev/null
    wait_point=$(python3 - "$xml" <<'PY'
import re, sys, xml.etree.ElementTree as ET
nodes = list(ET.parse(sys.argv[1]).iter("node"))
if any("System UI isn't responding" in node.attrib.get("text", "") for node in nodes):
    for node in nodes:
        if (node.attrib.get("resource-id", "").endswith("aerr_wait") or
                node.attrib.get("text", "") in ("Wait", "Poczekaj")):
            values = list(map(int, re.findall(r"\d+", node.attrib.get("bounds", ""))))
            if len(values) == 4:
                x1, y1, x2, y2 = values
                print((x1 + x2) // 2, (y1 + y2) // 2)
                break
PY
)
    [[ -z "$wait_point" ]] && return
    echo "Emulator System UI stalled; selecting Wait (attempt $attempt)" >&2
    adb shell input tap $wait_point
    sleep 3
  done
  adb shell uiautomator dump /sdcard/yourlm-window.xml >/dev/null
  adb pull /sdcard/yourlm-window.xml "$xml" >/dev/null
}
assert_text() {
  local expected=$1 attempt
  for attempt in 1 2 3 4 5; do
    pull_ui
    if python3 - "$xml" "$expected" <<'PY'
import sys, xml.etree.ElementTree as ET
texts = [node.attrib.get("text", "") for node in ET.parse(sys.argv[1]).iter("node")]
raise SystemExit(0 if any(sys.argv[2] in value for value in texts) else 1)
PY
    then
      return
    fi
    sleep 1
  done
  python3 - "$xml" "$expected" <<'PY'
import sys, xml.etree.ElementTree as ET
texts = [node.attrib.get("text", "") for node in ET.parse(sys.argv[1]).iter("node")]
raise SystemExit(f"missing UI text {sys.argv[2]!r}; visible={texts}")
PY
}
click_text() {
  local target=$1 point x y attempt=0
  local size dimensions screen_width screen_height scroll_x
  size=$(adb shell wm size | tr -d '\r' | tail -1)
  dimensions=${size##*: }
  screen_width=${dimensions%x*}
  screen_height=${dimensions##*x}
  [[ "$screen_width" =~ ^[0-9]+$ ]] || screen_width=1080
  [[ "$screen_height" =~ ^[0-9]+$ ]] || screen_height=1920
  scroll_x=$((screen_width / 2))
  while ((attempt < 8)); do
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
      echo "UI target '$target': $x,$y on height $screen_height (attempt $attempt)"
      if ((y >= 40 && y < screen_height - 110)); then
        adb shell input tap "$x" "$y"
        return
      fi
    fi
    adb shell input swipe "$scroll_x" $((screen_height * 4 / 5)) "$scroll_x" $((screen_height / 3)) 350
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
# Match the full button label: the instructional step above also begins with
# “Uruchom worker” and tapping that text does not start the service.
click_text "Uruchom worker w tle"

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
size=$(adb shell wm size | tr -d '\r' | tail -1)
dimensions=${size##*: }
screen_width=${dimensions%x*}
screen_height=${dimensions##*x}
[[ "$screen_width" =~ ^[0-9]+$ ]] || screen_width=1080
[[ "$screen_height" =~ ^[0-9]+$ ]] || screen_height=1920
adb shell input swipe $((screen_width / 2)) $((screen_height * 4 / 5)) $((screen_width / 2)) $((screen_height / 4)) 500
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

# The worker is service-owned: backgrounding the Activity and switching the
# display off must leave both the foreground service and native RPC process up.
adb shell input keyevent 3
sleep 3
adb shell dumpsys activity services pl.yourlm.node | grep -q 'WorkerService'
adb shell ps -A | grep -q 'libylm_rpc'
adb shell dumpsys notification --noredact | grep -q 'Worker YourLM'
adb shell input keyevent 26
sleep 5
adb shell dumpsys activity services pl.yourlm.node | grep -q 'WorkerService'
adb shell ps -A | grep -q 'libylm_rpc'
adb shell dumpsys power | grep -q 'YourLM:WorkerCPU'
adb shell input keyevent 26
adb shell wm dismiss-keyguard || true
adb shell am start -W -n pl.yourlm.node/.NodeActivity >/dev/null
sleep 2
pull_ui
python3 - "$xml" <<'PY'
import sys, xml.etree.ElementTree as ET
texts=[n.attrib.get("text","") for n in ET.parse(sys.argv[1]).iter("node")]
if not any("WORKER GOTOWY" in value or "Worker działa w tle" in value for value in texts):
    raise SystemExit(f"foreground worker was not restored in UI; visible={texts}")
PY

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
