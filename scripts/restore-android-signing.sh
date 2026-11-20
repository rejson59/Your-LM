#!/usr/bin/env bash
# Restore the stable Android update identity from two private draft releases.
# Draft releases are visible only to authenticated repository writers and must
# never be published. The split prevents one accidental publication from
# exposing both the encrypted PKCS12 and its passphrase.
set -euo pipefail

if (($# != 2)); then
  echo "usage: $0 KEYSTORE_PATH PASSWORD_FILE" >&2
  exit 2
fi
: "${GH_TOKEN:?GH_TOKEN with repository contents access is required}"
command -v gh >/dev/null || { echo "gh is required" >&2; exit 2; }

keystore=$1
password=$2
umask 077
mkdir -p "$(dirname "$keystore")" "$(dirname "$password")"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fetch_draft() {
  local tag=$1 output=$2
  gh api 'repos/rejson59/Your-LM/releases?per_page=100' \
    --jq ".[] | select(.draft == true and .tag_name == \"$tag\") | .body" > "$output"
  [[ -s "$output" ]] || { echo "Private signing backup $tag is unavailable" >&2; exit 1; }
}

fetch_draft private-yourlm-android-signing-key "$tmp/key"
fetch_draft private-yourlm-android-signing-passphrase "$tmp/pass"
sed -n '/^PKCS12_BASE64_BEGIN$/,/^PKCS12_BASE64_END$/p' "$tmp/key" |
  sed '1d;$d' | tr -d '\r\n' | base64 -d > "$keystore"
sed -n '/^PKCS12_PASSWORD_BEGIN$/,/^PKCS12_PASSWORD_END$/p' "$tmp/pass" |
  sed '1d;$d' | tr -d '\r\n' > "$password"
[[ -s "$keystore" && -s "$password" ]] || { echo "Private signing backup is malformed" >&2; exit 1; }
chmod 600 "$keystore" "$password"
