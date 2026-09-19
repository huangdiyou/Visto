#!/usr/bin/env bash
# Restore a native Linux or macOS Server data backup.
#
# Restore replaces the current data directory. It requires --confirm-restore so
# a stray invocation can never destroy live data. The archive is copied to a
# private staging directory first: hashing, validation, extraction and the
# restore path precheck then all read the same copy, which closes the
# time-of-check/time-of-use window where the host path could be swapped in
# between.
#
# The restore path precheck runs BEFORE the service is stopped. It compares the
# backup metadata against the target data directory and the database inside the
# archive; only a passing precheck reaches the swap. --check-only runs every
# check, reports whether the backup can be restored, and never touches the
# service or the data.

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=visto-server-env.sh
. "$script_dir/visto-server-env.sh"

visto_unix_detect_platform

backup_file=""
confirm_restore=0
check_only=0
max_expanded_bytes=$VISTO_UNIX_MAX_EXPANDED_BYTES_DEFAULT
max_entry_count=$VISTO_UNIX_MAX_ENTRY_COUNT_DEFAULT

usage() {
  cat <<'USAGE'
Usage: restore-visto-server.sh --backup-file <archive.tar.gz> [options]

  --backup-file <path>          Archive created by backup-visto-server.sh
  --confirm-restore             Required for a real restore. Acknowledges that
                                current data is replaced
  --check-only                  Validate the backup and report whether it can
                                be restored. Stops nothing, changes nothing
  --prefix <path>               Visto program prefix
  --config-dir <path>           Directory holding visto.env
  --max-expanded-bytes <bytes>  Expanded size limit (default: 107374182400)
  --max-entries <count>         Archive member count limit (default: 100000)
  -h, --help                    Show this help

The archive and its <archive>.json metadata must sit next to each other.
A restore only succeeds into the data directory the backup was taken from;
cross-directory restores are rejected before the service is stopped.
Exit status: 0 the backup can be restored, 11 the precheck rejected it,
1 anything else went wrong.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --backup-file) backup_file=${2:?--backup-file needs a value}; shift 2 ;;
    --confirm-restore) confirm_restore=1; shift ;;
    --check-only) check_only=1; shift ;;
    --prefix) VISTO_PREFIX=${2:?--prefix needs a value}; shift 2 ;;
    --config-dir) VISTO_CONFIG_DIR=${2:?--config-dir needs a value}; shift 2 ;;
    --max-expanded-bytes) max_expanded_bytes=${2:?--max-expanded-bytes needs a value}; shift 2 ;;
    --max-entries) max_entry_count=${2:?--max-entries needs a value}; shift 2 ;;
    -h | --help) usage; exit 0 ;;
    *) visto_unix_die "Unknown argument: $1" ;;
  esac
done

if [ -z "$backup_file" ]; then
  visto_unix_die "--backup-file is required."
fi
if [ "$check_only" = "1" ] && [ "$confirm_restore" = "1" ]; then
  visto_unix_die "--check-only and --confirm-restore are mutually exclusive."
fi
if [ "$confirm_restore" != "1" ] && [ "$check_only" != "1" ]; then
  visto_unix_die "Restore replaces the current Visto data directory. Re-run with --confirm-restore after verifying the backup file, or run with --check-only to validate the backup without changing anything."
fi
for limit in "$max_expanded_bytes" "$max_entry_count"; do
  if ! printf '%s' "$limit" | grep -Eq '^[0-9]+$' || [ "$limit" -le 0 ]; then
    visto_unix_die "Size and entry limits must be positive integers."
  fi
done

visto_unix_resolve_paths
visto_unix_require_root
visto_unix_read_env

if [ ! -f "$backup_file" ]; then
  visto_unix_die "Backup file was not found: $backup_file"
fi
if [ ! -d "$VISTO_DATA_DIR" ]; then
  visto_unix_die "Visto data directory was not found: $VISTO_DATA_DIR"
fi

staging_dir=$(mktemp -d)
trap 'rm -rf "$staging_dir"' EXIT

backup_name=$(basename "$backup_file")
visto_unix_assert_safe_name "backup file name" "$backup_name"
cp "$backup_file" "$staging_dir/$backup_name"
if [ ! -f "$backup_file.json" ]; then
  visto_unix_die "Backup metadata file is required: $backup_file.json"
fi
cp "$backup_file.json" "$staging_dir/$backup_name.json"

metadata_path="$staging_dir/$backup_name.json"
schema_version=$(visto_unix_read_metadata "$metadata_path" schemaVersion)
metadata_archive=$(visto_unix_read_metadata "$metadata_path" archive)
expected_hash=$(visto_unix_read_metadata "$metadata_path" sha256)
case "$schema_version" in
  1 | 2) ;;
  *)
    visto_unix_reject "Backup metadata schemaVersion ${schema_version:-unknown} is not supported by this restore script. Current data was not changed."
    ;;
esac
if [ "$metadata_archive" != "$backup_name" ]; then
  visto_unix_die "Backup metadata does not describe the selected archive."
fi
if ! printf '%s' "$expected_hash" | grep -Eq '^[a-fA-F0-9]{64}$'; then
  visto_unix_die "Backup metadata does not record a valid SHA-256."
fi
actual_hash=$(visto_unix_sha256 "$staging_dir/$backup_name" | tr 'A-Z' 'a-z')
expected_hash=$(printf '%s' "$expected_hash" | tr 'A-Z' 'a-z')
if [ "$actual_hash" != "$expected_hash" ]; then
  visto_unix_die "Backup SHA-256 does not match its metadata. Current data was not changed."
fi
# Legacy schemaVersion 1 backups record no source data directory, so no check
# can prove this restore lands back where the backup was taken. Refuse before
# anything is touched; do not fall back to assuming the current directory.
if [ "$schema_version" = "1" ]; then
  visto_unix_reject "$(visto_unix_legacy_restore_rejection)"
fi

# Entry guards, member types and expanded size, all against the staged copy.
# The measured backup size drives the space assertion; the static limit stays
# the rejection cap so a huge archive is still refused before extraction.
backup_names=$(tar -tzf "$staging_dir/$backup_name") || visto_unix_die "Backup archive could not be read."
backup_listing=$(tar -tzvf "$staging_dir/$backup_name") || visto_unix_die "Backup archive could not be read."
backup_bytes=$(printf '%s\n' "$backup_listing" | visto_unix_measure_archive_bytes "$max_expanded_bytes")
[ "${backup_bytes%% *}" = "OK" ] || visto_unix_die "Backup archive rejected (${backup_bytes#ERR })."
space_required=$(( ${backup_bytes##* } * 2 ))
[ "$space_required" -lt 268435456 ] && space_required=268435456
visto_unix_assert_free_space "$(dirname "$VISTO_DATA_DIR")" "$space_required"
entry_count=$(printf '%s\n' "$backup_names" | awk 'NF { count++ } END { print count + 0 }')
if [ "$entry_count" -gt "$max_entry_count" ]; then
  visto_unix_die "Backup archive exceeds the entry limit of ${max_entry_count}. Current data was not changed."
fi
while IFS= read -r entry; do
  [ -n "$entry" ] || continue
  visto_unix_assert_archive_entry "$entry"
done <<< "$backup_names"

extract_dir="$staging_dir/extracted"
mkdir -p "$extract_dir"
tar -xzf "$staging_dir/$backup_name" -C "$extract_dir" ||
  visto_unix_die "Backup extraction failed. Current data was not changed."
extracted_bytes=$(du -sk "$extract_dir" | awk '{ print $1 * 1024 }')
if [ "$extracted_bytes" -gt "$max_expanded_bytes" ]; then
  visto_unix_die "Backup expands beyond the configured limit of ${max_expanded_bytes} bytes. Current data was not changed."
fi

# Restore path precheck: read the database and storage roots from the isolated
# copy and compare them against the metadata and the target data directory.
# This runs BEFORE the service is stopped; a rejection here leaves the running
# service, its configuration and its data completely untouched.
server_binary=$(visto_unix_release_binary)
if [ ! -x "$server_binary" ]; then
  visto_unix_die "Restore precheck could not run: ${server_binary} is missing or not executable. Current data was not changed."
fi
set +e
precheck_output=$("$server_binary" backup precheck \
  --metadata "$metadata_path" \
  --staged-data-dir "$extract_dir/$(basename "$VISTO_DATA_DIR")" \
  --target-data-dir "$VISTO_DATA_DIR" 2>&1)
precheck_status=$?
set -e
[ -n "$precheck_output" ] && printf '%s\n' "$precheck_output"
case "$precheck_status" in
  0) ;;
  11)
    if [ "$check_only" = "1" ]; then
      visto_unix_reject "Restore precheck rejected this backup (see the reasons above). No service was stopped and no data was changed."
    fi
    visto_unix_reject "Restore precheck rejected this backup (see the reasons above). Current data was not changed."
    ;;
  *)
    visto_unix_die "Restore precheck could not run (exit ${precheck_status}). Current data was not changed."
    ;;
esac

if [ "$check_only" = "1" ]; then
  visto_unix_info "Check-only precheck passed: this backup can be restored into ${VISTO_DATA_DIR}."
  visto_unix_info "No service was stopped and no data was changed."
  exit 0
fi

visto_unix_info "Stopping ${VISTO_SERVICE_NAME}."
if ! visto_unix_service_stop; then
  visto_unix_die "Restore was not started because ${VISTO_SERVICE_NAME} could not be stopped. Current data was not changed."
fi

preserved_dir="${VISTO_DATA_DIR}.pre-restore-$(visto_unix_timestamp)"
if ! mv "$VISTO_DATA_DIR" "$preserved_dir"; then
  visto_unix_die "Could not preserve the current data directory. Current data was not changed."
fi

restore_previous() {
  if [ -d "$preserved_dir" ]; then
    # The previous data is only put back over a confirmed stop; otherwise the
    # restored data stays in place and the preserved copy is kept for a manual
    # recovery instead of a half-replaced directory being started.
    if ! visto_unix_service_stop; then
      visto_unix_die "Could not stop ${VISTO_SERVICE_NAME} to put the previous data back. The restored data was left at ${VISTO_DATA_DIR} and the previous directory is preserved at ${preserved_dir}. Recover manually."
    fi
    rm -rf "$VISTO_DATA_DIR"
    mv "$preserved_dir" "$VISTO_DATA_DIR"
  fi
  visto_unix_service_start >/dev/null 2>&1 || true
}

if ! mv "$extract_dir/$(basename "$VISTO_DATA_DIR")" "$VISTO_DATA_DIR"; then
  visto_unix_warn "Could not move the restored data into place. Restoring the previous directory."
  restore_previous
  visto_unix_die "Restore failed; the previous data was restored."
fi

if visto_unix_service_start >/dev/null 2>&1 && visto_unix_wait_ready; then
  visto_unix_info "Restore completed and Visto Server is answering on ${VISTO_SERVER_ADDRESS}."
  visto_unix_info "Previous data kept at ${preserved_dir}; remove it only after verifying the restore."
  exit 0
fi

visto_unix_warn "Visto Server did not become healthy after the restore. Restoring the previous directory."
if ! visto_unix_service_stop; then
  visto_unix_die "Could not stop ${VISTO_SERVICE_NAME} to put the previous data back. The restored data was left at ${VISTO_DATA_DIR} and the previous directory is preserved at ${preserved_dir}. Recover manually."
fi
rm -rf "$VISTO_DATA_DIR"
mv "$preserved_dir" "$VISTO_DATA_DIR"
visto_unix_service_start >/dev/null 2>&1 || true
visto_unix_wait_ready >/dev/null 2>&1
visto_unix_die "Restore failed; the previous data was restored."
