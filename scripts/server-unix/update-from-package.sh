#!/usr/bin/env bash
# Read the official checksum sidecar and delegate to the existing protected updater.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
. "${SCRIPT_DIR}/visto-server-env.sh"
PACKAGE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --package) PACKAGE="${2:?--package requires the downloaded archive}"; shift 2 ;;
    -h|--help) printf '%s\n' 'sudo bash scripts/update-from-package.sh --package /path/to/package.tar.gz'; exit 0 ;;
    *) visto_unix_die "unknown option: $1" ;;
  esac
done
[ -f "$PACKAGE" ] || visto_unix_die "Choose the downloaded Server archive."
[ -f "${PACKAGE}.sha256" ] || visto_unix_die "The checksum file is missing. Download the complete update from the official release."
[ -f "${PACKAGE}.manifest.json" ] || visto_unix_die "The version manifest is missing. Download the complete update from the official release."
[ "$(wc -l < "${PACKAGE}.sha256" | tr -d ' ')" -le 1 ] || visto_unix_die "The checksum file has multiple records."
IFS=' ' read -r DIGEST NAME < "${PACKAGE}.sha256" || [ -n "${DIGEST:-}" ]
[[ "$DIGEST" =~ ^[[:xdigit:]]{64}$ ]] || visto_unix_die "The checksum file is invalid."
NAME=${NAME#\*}
[ "$NAME" = "$(basename "$PACKAGE")" ] || visto_unix_die "The checksum belongs to a different archive."
exec bash "${SCRIPT_DIR}/update-visto-server.sh" \
  --package "$PACKAGE" --sha256 "$DIGEST" --manifest "${PACKAGE}.manifest.json"
