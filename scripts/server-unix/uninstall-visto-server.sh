#!/usr/bin/env bash
#
# Uninstall the native Visto Server and deregister its launchd service.
#
# Uninstall removes the service registration and the installed releases, and
# deliberately KEEPS user data: the data directory, backups and logs survive so
# a reinstall picks the workspace back up. Pass --purge-data only when the
# administrator explicitly wants the data removed as well.
#
# Usage:
#   sudo bash scripts/server-unix/uninstall-visto-server.sh [options]
#
# Options:
#   --keep-releases    remove the service but leave the installed releases
#   --purge-data       also delete the data and backup directories (DESTRUCTIVE)
#   --yes              do not ask for confirmation when purging data
#
# The directory prefix, service label and plist path honour VISTO_PREFIX,
# VISTO_SERVICE_NAME and VISTO_LAUNCHD_PLIST.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"

# shellcheck source=/dev/null
. "${SCRIPT_DIR}/visto-server-env.sh"

KEEP_RELEASES=0
PURGE_DATA=0
ASSUME_YES=0

while [ $# -gt 0 ]; do
  case "$1" in
    --keep-releases) KEEP_RELEASES=1; shift ;;
    --purge-data) PURGE_DATA=1; shift ;;
    --yes) ASSUME_YES=1; shift ;;
    -h | --help) sed -n '2,20p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) visto_unix_error "unknown option: $1"; exit 2 ;;
  esac
done

visto_unix_detect_platform
visto_unix_resolve_paths
visto_unix_require_root

visto_unix_info "uninstalling Visto Server"
visto_unix_info "  prefix:  ${VISTO_PREFIX}"
visto_unix_info "  data:    ${VISTO_DATA_DIR_DEFAULT}"
visto_unix_info "  service: ${VISTO_SERVICE_NAME}"

# --- Stop and deregister ----------------------------------------------------
visto_unix_service_stop 2>/dev/null || visto_unix_warn "service was not running or could not be stopped"
if [ -f "$VISTO_LAUNCHD_PLIST" ]; then
  rm -f "$VISTO_LAUNCHD_PLIST"
  visto_unix_info "removed ${VISTO_LAUNCHD_PLIST}"
fi

# --- Remove releases --------------------------------------------------------
if [ "$KEEP_RELEASES" = "1" ]; then
  visto_unix_info "keeping installed releases under ${VISTO_RELEASES_DIR}"
else
  rm -f "$VISTO_CURRENT_LINK" 2>/dev/null || true
  rm -rf "$VISTO_RELEASES_DIR" 2>/dev/null || true
  visto_unix_info "removed ${VISTO_RELEASES_DIR}"
fi

# --- Data retention ---------------------------------------------------------
if [ "$PURGE_DATA" = "1" ]; then
  if [ "$ASSUME_YES" != "1" ]; then
    printf 'This deletes %s and %s. Type PURGE to continue: ' "$VISTO_DATA_DIR_DEFAULT" "$VISTO_BACKUP_DIR" >&2
    read -r reply
    [ "$reply" = PURGE ] || visto_unix_die "aborted; data was left untouched"
  fi
  rm -rf "$VISTO_DATA_DIR_DEFAULT" "$VISTO_BACKUP_DIR"
  visto_unix_info "purged data and backups"
else
  visto_unix_info "kept data:      ${VISTO_DATA_DIR_DEFAULT}"
  visto_unix_info "kept backups:   ${VISTO_BACKUP_DIR}"
  visto_unix_info "kept logs:      ${VISTO_LOG_DIR}"
fi

visto_unix_info "uninstall complete"
