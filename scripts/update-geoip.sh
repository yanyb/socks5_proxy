#!/usr/bin/env bash
# Download / refresh GeoLite2-City.mmdb used by the server's heartbeat enricher.
#
# Source: https://github.com/wp-statistics/GeoLite2-City  (CC BY-SA 4.0, MaxMind)
# CDN:    https://cdn.jsdelivr.net/npm/geolite2-city/GeoLite2-City.mmdb.gz
#
# After this script succeeds, send SIGHUP to the running server to swap the
# in-memory reader without dropping device sessions:
#   kill -HUP $(pgrep -f xsocks5-server)

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST_DIR="${GEOIP_DEST_DIR:-${ROOT_DIR}/server/data}"
DEST_FILE="${DEST_DIR}/GeoLite2-City.mmdb"
TMP_FILE="$(mktemp "${DEST_DIR}/.GeoLite2-City.mmdb.XXXXXX")"

URL="${GEOIP_URL:-https://cdn.jsdelivr.net/npm/geolite2-city/GeoLite2-City.mmdb.gz}"

mkdir -p "${DEST_DIR}"

cleanup() { rm -f "${TMP_FILE}.gz" "${TMP_FILE}"; }
trap cleanup EXIT

echo "downloading ${URL}"
curl --fail --silent --show-error --location \
  --output "${TMP_FILE}.gz" \
  "${URL}"

echo "decompressing"
gunzip -c "${TMP_FILE}.gz" > "${TMP_FILE}"

# Sanity: GeoLite2-City is ~60-90 MiB uncompressed.
SIZE_BYTES=$(wc -c < "${TMP_FILE}")
if [ "${SIZE_BYTES}" -lt 10000000 ]; then
  echo "ERROR: downloaded file too small (${SIZE_BYTES} bytes), refusing to install" >&2
  exit 1
fi

echo "installing -> ${DEST_FILE} (${SIZE_BYTES} bytes)"
mv "${TMP_FILE}" "${DEST_FILE}"
trap - EXIT
rm -f "${TMP_FILE}.gz"

echo "done. send SIGHUP to xsocks5-server to hot-reload (e.g. kill -HUP <pid>)."
