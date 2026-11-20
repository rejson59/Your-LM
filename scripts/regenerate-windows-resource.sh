#!/usr/bin/env bash
# Regenerate the deterministic PE manifest/version resource committed in host/.
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
GOBIN="$tmp" go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@fc50c1956fff0b5bb87215d44b7f73c06996d8e9
"$tmp/goversioninfo" -64 -manifest windows/YourLM.manifest \
  -o host/resource_windows_amd64.syso windows/versioninfo.json
