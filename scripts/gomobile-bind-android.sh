#!/usr/bin/env bash
# Build my_socks5_proxy/mobile as an Android AAR (requires Android SDK + NDK, gomobile, gobind).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
ANDROID_HOME=/Users/pengshanshan/Library/Android/sdk
: "${ANDROID_HOME:?Set ANDROID_HOME to your Android SDK path}"
if [[ -z "${ANDROID_NDK_HOME:-}" && -d "${ANDROID_HOME}/ndk" ]]; then
  ANDROID_NDK_HOME="${ANDROID_HOME}/ndk/$(ls "${ANDROID_HOME}/ndk" 2>/dev/null | sort -V | tail -1)"
fi
: "${ANDROID_NDK_HOME:?Set ANDROID_NDK_HOME or install an NDK under \$ANDROID_HOME/ndk/<version>}"
export ANDROID_NDK_HOME

OUT="${1:-$ROOT/mobile/build/deviceclient.aar}"
mkdir -p "$(dirname "$OUT")"

JAVAPREFIX="${JAVAPREFIX:-my.socks5.proxy}"

gomobile bind \
  -target=android \
  -androidapi=21 \
  -javapkg="${JAVAPREFIX}" \
  -o "${OUT}" \
  ./mobile

echo "Wrote ${OUT}"
