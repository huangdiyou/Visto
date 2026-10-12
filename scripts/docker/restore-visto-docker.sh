#!/usr/bin/env bash
# / restore a Visto Docker data volume backup.
#
# Restore replaces the current data volume. It requires --confirm-restore so a
# stray invocation can never destroy live data. The archive is copied to a
# private staging directory first: hashing, validation, extraction and the
# restore path precheck then all read the same copy, which closes the
# time-of-check/time-of-use window where the host path could be swapped in
# between.
#
# The restore path precheck runs inside the Core image BEFORE the stack is
# stopped. It compares the backup metadata against the running container's data
# directory and the database inside the archive; only a passing precheck
# reaches the switch. --check-only runs every check, reports whether the backup
# can be restored, and never touches the stack or the data volume.

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repository_root=$(cd "$script_dir/../.." && pwd)
# shellcheck source=visto-docker.sh
. "$script_dir/visto-docker.sh"

compose_file="$repository_root/compose.yaml"
project_name="visto"
backup_file=""
confirm_restore=0
check_only=0
max_expanded_bytes=$VISTO_DOCKER_MAX_EXPANDED_BYTES_DEFAULT
max_entry_count=$VISTO_DOCKER_MAX_ENTRY_COUNT_DEFAULT

usage() {
  cat <<'USAGE'
Usage: restore-visto-docker.sh --backup-file <archive.tar.gz> [options]

  --backup-file <path>          Backup archive created by backup-visto-docker.sh
  --confirm-restore             Required for a real restore. Acknowledges that
                                current data is replaced
  --check-only                  Validate the backup and report whether it can
                                be restored. Stops nothing, changes nothing
  --compose-file <path>         Compose file to operate on (default: repository compose.yaml)
  --project-name <name>         Compose project name (default: visto)
  --max-expanded-bytes <bytes>  Expanded size limit (default: 107374182400)
  --max-entries <count>         Archive member count limit (default: 100000)
  -h, --help                    Show this help

The archive and its <archive>.tar.gz.json metadata must sit next to each other.
A restore only succeeds into the compose project and data volume the backup was
taken from; other targets are rejected before the stack is stopped.
Exit status: 0 the backup can be restored, 11 the precheck rejected it,
1 anything else went wrong.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --backup-file) backup_file=${2:?--backup-file needs a value}; shift 2 ;;
    --confirm-restore) confirm_restore=1; shift ;;
    --check-only) check_only=1; shift ;;
    --compose-file) compose_file=${2:?--compose-file needs a value}; shift 2 ;;
    --project-name) project_name=${2:?--project-name needs a value}; shift 2 ;;
    --max-expanded-bytes) max_expanded_bytes=${2:?--max-expanded-bytes needs a value}; shift 2 ;;
    --max-entries) max_entry_count=${2:?--max-entries needs a value}; shift 2 ;;
    -h | --help) usage; exit 0 ;;
    *) visto_docker_die "Unknown argument: $1" ;;
  esac
done

if [ -z "$backup_file" ]; then
  visto_docker_die "--backup-file is required."
fi
if [ "$check_only" = "1" ] && [ "$confirm_restore" = "1" ]; then
  visto_docker_die "--check-only and --confirm-restore are mutually exclusive."
fi
if [ "$confirm_restore" != "1" ] && [ "$check_only" != "1" ]; then
  visto_docker_die "Restore replaces the current Visto data volume. Re-run with --confirm-restore after verifying the backup file, or run with --check-only to validate the backup without changing anything."
fi
if ! printf '%s' "$max_expanded_bytes" | grep -Eq '^[0-9]+$' || [ "$max_expanded_bytes" -le 0 ]; then
  visto_docker_die "--max-expanded-bytes must be a positive integer."
fi
if ! printf '%s' "$max_entry_count" | grep -Eq '^[0-9]+$' || [ "$max_entry_count" -le 0 ]; then
  visto_docker_die "--max-entries must be a positive integer."
fi
visto_docker_assert_safe_name "project name" "$project_name"
visto_docker_assert_ready "$compose_file"

if [ ! -f "$backup_file" ]; then
  visto_docker_die "Backup file was not found: $backup_file"
fi
backup_file=$(cd "$(dirname "$backup_file")" && printf '%s/%s' "$(pwd)" "$(basename "$backup_file")")

staging_dir=$(mktemp -d "${TMPDIR:-/tmp}/visto-restore-XXXXXX")
suffix=$(basename "$staging_dir" | tr -cd 'A-Za-z0-9' | tr 'A-Z' 'a-z')
stage_volume="visto-restore-stage-$suffix"
rollback_volume="visto-restore-rollback-$suffix"
rollback_created=0

cleanup() {
  docker volume rm -f "$stage_volume" >/dev/null 2>&1 || true
  if [ "$rollback_created" = "1" ]; then
    docker volume rm -f "$rollback_volume" >/dev/null 2>&1 || true
  fi
  rm -rf "$staging_dir"
}
trap cleanup EXIT

backup_name=$(basename "$backup_file")
visto_docker_assert_safe_name "backup file name" "$backup_name"
cp "$backup_file" "$staging_dir/$backup_name"
if [ ! -f "$backup_file.json" ]; then
  visto_docker_die "Backup metadata file is required: $backup_file.json"
fi
cp "$backup_file.json" "$staging_dir/$backup_name.json"

metadata_path="$staging_dir/$backup_name.json"
schema_version=$(visto_docker_read_metadata "$metadata_path" schemaVersion)
metadata_archive=$(visto_docker_read_metadata "$metadata_path" archive)
expected_hash=$(visto_docker_read_metadata "$metadata_path" sha256)
case "$schema_version" in
  1 | 2) ;;
  *)
    visto_docker_reject "Backup metadata schemaVersion ${schema_version:-unknown} is not supported by this restore script. Current data was not changed."
    ;;
esac
if [ "$metadata_archive" != "$backup_name" ]; then
  visto_docker_die "Backup metadata does not describe the selected archive."
fi
if ! printf '%s' "$expected_hash" | grep -Eq '^[a-fA-F0-9]{64}$'; then
  visto_docker_die "Backup metadata does not record a valid SHA-256."
fi

# tr keeps this working on macOS, whose default bash is 3.2 and has no ${var,,}.
actual_hash=$(visto_docker_sha256 "$staging_dir/$backup_name" | tr 'A-Z' 'a-z')
expected_hash=$(printf '%s' "$expected_hash" | tr 'A-Z' 'a-z')
if [ "$actual_hash" != "$expected_hash" ]; then
  visto_docker_die "Backup SHA-256 does not match its metadata. Current data was not changed."
fi
# Legacy schemaVersion 1 backups record no source data directory, so no check
# can prove this restore lands back where the backup was taken. Refuse before
# anything is touched; do not fall back to assuming the current volume.
if [ "$schema_version" = "1" ]; then
  visto_docker_reject "$(visto_docker_legacy_restore_rejection)"
fi

volume=$(visto_docker_data_volume "$compose_file" "$project_name")
utility_image=$(visto_docker_utility_image "$compose_file" "$project_name")
visto_docker_assert_safe_name "data volume" "$volume"
data_dir=$(visto_docker_data_dir "$compose_file" "$project_name")

# A schemaVersion 2 backup records the compose project, the data volume and
# the container data directory it was taken from. A different target is a
# different instance, not a restore target.
metadata_project=$(visto_docker_read_metadata "$metadata_path" composeProject)
metadata_volume=$(visto_docker_read_metadata "$metadata_path" dataVolume)
if [ -n "$metadata_project" ] && [ "$metadata_project" != "$project_name" ]; then
  visto_docker_reject "Backup metadata records compose project ${metadata_project} but this restore targets ${project_name}. Current data was not changed."
fi
if [ -n "$metadata_volume" ] && [ "$metadata_volume" != "$volume" ]; then
  visto_docker_reject "Backup metadata records data volume ${metadata_volume} but this restore targets ${volume}. Current data was not changed."
fi

visto_docker_validate_archive \
  "$utility_image" "$staging_dir" "$backup_name" "$max_expanded_bytes" "$max_entry_count"

docker volume create "$stage_volume" >/dev/null ||
  visto_docker_die "Could not create restore staging volume."

visto_docker_run_tar \
  --volume "$stage_volume:/stage" \
  --volume "$staging_dir:/backup:ro" \
  "$utility_image" -xzf "/backup/$backup_name" -C /stage ||
  visto_docker_die "Backup extraction into the staging volume failed. Current data was not changed."

# Restore path precheck inside the Core image, against the isolated staging
# volume and the running container's data directory. This runs BEFORE the stack
# is stopped; a rejection leaves the stack, its configuration and its data
# volume completely untouched.
set +e
precheck_output=$(visto_docker_run_server \
  --volume "$stage_volume:/stage" \
  --volume "$staging_dir:/backup:ro" \
  "$utility_image" backup precheck \
  --metadata "/backup/$backup_name.json" \
  --staged-data-dir /stage \
  --target-data-dir "$data_dir" 2>&1)
precheck_status=$?
set -e
[ -n "$precheck_output" ] && printf '%s\n' "$precheck_output"
case "$precheck_status" in
  0) ;;
  11)
    if [ "$check_only" = "1" ]; then
      visto_docker_reject "Restore precheck rejected this backup (see the reasons above). No stack was stopped and no data was changed."
    fi
    visto_docker_reject "Restore precheck rejected this backup (see the reasons above). Current data was not changed."
    ;;
  *)
    visto_docker_die "Restore precheck could not run (exit ${precheck_status}). Current data was not changed."
    ;;
esac

if [ "$check_only" = "1" ]; then
  visto_docker_info "Check-only precheck passed: this backup can be restored into data volume ${volume}."
  visto_docker_info "No stack was stopped and no data was changed."
  exit 0
fi

visto_docker_info "Stopping the stack and preserving the current data volume."
visto_docker_compose "$compose_file" "$project_name" down >/dev/null
docker volume create "$rollback_volume" >/dev/null ||
  visto_docker_die "Could not create rollback volume."
visto_docker_run_sh \
  --volume "$volume:/source:ro" \
  --volume "$rollback_volume:/target" \
  "$utility_image" -c 'tar -C /source -cf - . | tar -C /target -xf -' ||
  visto_docker_die "Could not preserve the current data volume."
rollback_created=1

if ! visto_docker_run_sh \
  --volume "$volume:/target" \
  --volume "$stage_volume:/source:ro" \
  "$utility_image" -c 'rm -rf /target/* /target/.[!.]* /target/..?* 2>/dev/null || true; tar -C /source -cf - . | tar -C /target -xf -'; then
  visto_docker_warn "Could not switch to the staged restore data. Rolling back to the preserved volume."
  visto_docker_run_sh \
    --volume "$volume:/target" \
    --volume "$rollback_volume:/source:ro" \
    "$utility_image" -c 'rm -rf /target/* /target/.[!.]* /target/..?* 2>/dev/null || true; tar -C /source -cf - . | tar -C /target -xf -' ||
    visto_docker_die "Restore failed and the preserved volume could not be copied back. Do not discard $backup_file."
  visto_docker_compose "$compose_file" "$project_name" up -d >/dev/null ||
    visto_docker_die "Restore rolled back but the stack could not be started. Start it manually and check health."
  visto_docker_wait_healthy "$compose_file" "$project_name" >/dev/null ||
    visto_docker_die "Restore rolled back but Core is not healthy. Inspect the logs before continuing."
  visto_docker_die "Restore failed; the previous data was restored and Core is healthy."
fi

visto_docker_compose "$compose_file" "$project_name" up -d >/dev/null ||
  visto_docker_die "Restore switched the data volume but the stack could not be started."
if ! visto_docker_wait_healthy "$compose_file" "$project_name"; then
  visto_docker_warn "Core did not become healthy after the restore. Rolling back to the preserved volume."
  visto_docker_compose "$compose_file" "$project_name" down >/dev/null
  visto_docker_run_sh \
    --volume "$volume:/target" \
    --volume "$rollback_volume:/source:ro" \
    "$utility_image" -c 'rm -rf /target/* /target/.[!.]* /target/..?* 2>/dev/null || true; tar -C /source -cf - . | tar -C /target -xf -' ||
    visto_docker_die "Restore failed and the preserved volume could not be copied back. Do not discard $backup_file."
  visto_docker_compose "$compose_file" "$project_name" up -d >/dev/null
  visto_docker_wait_healthy "$compose_file" "$project_name" >/dev/null
  visto_docker_die "Restore failed; the previous data was restored and Core is healthy."
fi

visto_docker_info "Restore completed and Core is healthy."
