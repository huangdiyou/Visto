#!/usr/bin/env bash
# Back up the native Linux or macOS Server data directory.
#
# Run this on the deployment host as root before any update or restore. The
# service is stopped so SQLite, WAL, secrets, uploads and preview artifacts are
# archived from a quiesced directory, then started again and waited on until it
# answers on its configured address.

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=visto-server-env.sh
. "$script_dir/visto-server-env.sh"

visto_unix_detect_platform

custom_backup_dir=""

usage() {
  cat <<'USAGE'
Usage: backup-visto-server.sh [options]

  --backup-dir <path>   Directory for the archive and its metadata
  --prefix <path>       Visto program prefix (default: /opt/visto, macOS /Library/Visto)
  --config-dir <path>   Directory holding visto.env
  -h, --help            Show this help

Prints "Backup created: <archive>" so the update script can pick the archive up.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --backup-dir) custom_backup_dir=${2:?--backup-dir needs a value}; shift 2 ;;
    --prefix) VISTO_PREFIX=${2:?--prefix needs a value}; shift 2 ;;
    --config-dir) VISTO_CONFIG_DIR=${2:?--config-dir needs a value}; shift 2 ;;
    -h | --help) usage; exit 0 ;;
    *) visto_unix_die "Unknown argument: $1" ;;
  esac
done

visto_unix_resolve_paths
[ -n "$custom_backup_dir" ] && VISTO_BACKUP_DIR="$custom_backup_dir"
visto_unix_require_root
visto_unix_read_env

if [ ! -d "$VISTO_DATA_DIR" ]; then
  visto_unix_die "Visto data directory was not found: $VISTO_DATA_DIR"
fi

mkdir -p "$VISTO_BACKUP_DIR" "$VISTO_LOG_DIR"
chmod 700 "$VISTO_BACKUP_DIR"

archive_name="visto-server-data-$(visto_unix_timestamp)-$$.tar.gz"
visto_unix_assert_safe_name "archive name" "$archive_name"
archive_path="$VISTO_BACKUP_DIR/$archive_name"
metadata_path="$archive_path.json"

# The restart is only armed once the service was really stopped: a refused or
# unconfirmed stop must not bootstrap another run over a data directory that may
# still be in use.
service_stopped=0

restart_service() {
  if [ "$service_stopped" != "1" ]; then
    return 0
  fi
  visto_unix_service_start >/dev/null 2>&1 ||
    visto_unix_warn "Visto Server could not be started after the backup. Start it manually."
  visto_unix_wait_ready >/dev/null 2>&1 ||
    visto_unix_warn "Visto Server did not answer on ${VISTO_SERVER_ADDRESS} after the backup."
}
trap restart_service EXIT

visto_unix_info "Stopping ${VISTO_SERVICE_NAME} to quiesce the data directory."
if ! visto_unix_service_stop; then
  visto_unix_die "Backup was not started because ${VISTO_SERVICE_NAME} could not be stopped; the data directory was not archived and nothing was restarted."
fi
service_stopped=1

tar -czf "$archive_path" -C "$(dirname "$VISTO_DATA_DIR")" "$(basename "$VISTO_DATA_DIR")" ||
  visto_unix_die "Could not create the backup archive."
if [ ! -f "$archive_path" ]; then
  visto_unix_die "Could not create the backup archive."
fi

visto_unix_write_backup_metadata \
  "$metadata_path" "$archive_name" \
  "$(visto_unix_sha256 "$archive_path")" "$(basename "$VISTO_DATA_DIR")"
chmod 600 "$archive_path" "$metadata_path"

visto_unix_info "Backup created: $archive_path"
visto_unix_info "Metadata: $metadata_path"
