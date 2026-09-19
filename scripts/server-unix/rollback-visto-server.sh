#!/usr/bin/env bash
# Roll a native Linux or macOS Visto Server back to a previous release.
#
# The update script keeps the previous release on disk, so a rollback is a
# symlink switch plus a restart. Data is backed up first because the older
# binary must never be started against a schema it cannot read: if the rollback
# fails, that backup is the recovery point.

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=visto-server-env.sh
. "$script_dir/visto-server-env.sh"

visto_unix_detect_platform

target_version=""
confirm_rollback=0
health_timeout=$VISTO_UNIX_HEALTH_TIMEOUT_DEFAULT

usage() {
  cat <<'USAGE'
Usage: rollback-visto-server.sh --confirm-rollback [options]

  --confirm-rollback         Required. Acknowledges that the active version changes
  --to-version <version>     Release to activate (default: newest release that is not active)
  --prefix <path>            Visto program prefix
  --config-dir <path>        Directory holding visto.env
  --health-timeout <seconds> Health wait after restart (default: 90)
  -h, --help                 Show this help

Releases are immutable and kept under <prefix>/releases/<version>.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --confirm-rollback) confirm_rollback=1; shift ;;
    --to-version) target_version=${2:?--to-version needs a value}; shift 2 ;;
    --prefix) VISTO_PREFIX=${2:?--prefix needs a value}; shift 2 ;;
    --config-dir) VISTO_CONFIG_DIR=${2:?--config-dir needs a value}; shift 2 ;;
    --health-timeout) health_timeout=${2:?--health-timeout needs a value}; shift 2 ;;
    -h | --help) usage; exit 0 ;;
    *) visto_unix_die "Unknown argument: $1" ;;
  esac
done

if [ "$confirm_rollback" != "1" ]; then
  visto_unix_die "Rollback changes the active Visto release. Re-run with --confirm-rollback."
fi
if ! printf '%s' "$health_timeout" | grep -Eq '^[0-9]+$' || [ "$health_timeout" -le 0 ]; then
  visto_unix_die "--health-timeout must be a positive integer."
fi

visto_unix_resolve_paths
visto_unix_require_root
visto_unix_read_env

active_version=$(visto_unix_current_version)
active_release=$(visto_unix_current_release)

if [ -z "$target_version" ]; then
  for candidate in $(visto_unix_list_releases); do
    if [ "$candidate" != "$active_version" ]; then
      target_version="$candidate"
      break
    fi
  done
fi
if [ -z "$target_version" ]; then
  visto_unix_die "No other release is available to roll back to."
fi
visto_unix_assert_version "$target_version"
if [ "$target_version" = "$active_version" ]; then
  visto_unix_die "Version ${target_version} is already active."
fi

target_release="$VISTO_RELEASES_DIR/$target_version"
if [ ! -d "$target_release" ]; then
  visto_unix_die "Release ${target_version} was not found at ${target_release}."
fi
if [ ! -x "$target_release/bin/visto-server" ]; then
  visto_unix_die "Release ${target_version} is incomplete; bin/visto-server is missing."
fi

mkdir -p "$VISTO_LOG_DIR" "$VISTO_RECOVERY_DIR"
chmod 700 "$VISTO_RECOVERY_DIR"

visto_unix_info "Backing up the data directory before rolling back."
backup_output=$("$script_dir/backup-visto-server.sh" \
  --backup-dir "$VISTO_BACKUP_DIR" \
  --prefix "$VISTO_PREFIX" \
  --config-dir "$VISTO_CONFIG_DIR") ||
  visto_unix_die "Rollback was not started because the pre-rollback backup failed."
data_backup=$(printf '%s\n' "$backup_output" | sed -n 's/^Backup created: //p' | awk 'NF { print; exit }')
visto_unix_info "Pre-rollback backup: ${data_backup:-unavailable}"

restore_active() {
  # Recovery replaces data and restarts the older release, so it needs a
  # confirmed stop too. Without one nothing is switched and nothing is started;
  # the data and the pre-rollback backup are preserved for a manual recovery.
  if ! visto_unix_service_stop; then
    visto_unix_die "Rollback recovery could not stop ${VISTO_SERVICE_NAME}. The active release was left unchanged and the pre-rollback backup at ${data_backup} is preserved."
  fi
  visto_unix_switch_release "$active_release" || true
  "$script_dir/restore-visto-server.sh" --backup-file "$data_backup" \
    --prefix "$VISTO_PREFIX" --config-dir "$VISTO_CONFIG_DIR" --confirm-restore ||
    visto_unix_die "Rollback recovery failed; active version remains stopped. Recover from ${data_backup}."
}

visto_unix_info "Stopping ${VISTO_SERVICE_NAME}."
if ! visto_unix_service_stop; then
  visto_unix_die "Rollback was not started because ${VISTO_SERVICE_NAME} could not be stopped. No release was switched; the pre-rollback backup at ${data_backup} is preserved."
fi

if ! visto_unix_switch_release "$target_release"; then
  restore_active
  visto_unix_die "Rollback failed; ${active_version} is still active."
fi

if ! visto_unix_service_start; then
  visto_unix_warn "The service did not start on ${target_version}. Restoring ${active_version}."
  restore_active
  visto_unix_die "Rollback failed; ${active_version} is active again."
fi

if ! visto_unix_wait_ready "$VISTO_SERVER_ADDRESS" "$health_timeout"; then
  visto_unix_warn "${target_version} did not answer within ${health_timeout} seconds. Restoring ${active_version}."
  restore_active
  visto_unix_die "Rollback failed; ${active_version} is active again. Investigate ${VISTO_LOG_DIR} before retrying."
fi

visto_unix_info "Rollback completed: ${active_version} -> ${target_version}."
if [ -n "$data_backup" ]; then
  visto_unix_info "If the older release cannot read the current data, restore: ${data_backup}"
fi
