#!/usr/bin/env bash
# back up the Visto Docker data volume.
#
# Run this on the deployment host before any update or restore. Core is stopped
# so SQLite, WAL, secrets, uploads and preview artifacts are archived from a
# quiesced volume, then started again and waited on until it is healthy.

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repository_root=$(cd "$script_dir/../.." && pwd)
# shellcheck source=visto-docker.sh
. "$script_dir/visto-docker.sh"

compose_file="$repository_root/compose.yaml"
project_name="visto"
backup_dir="$(pwd)/backups"

usage() {
  cat <<'USAGE'
Usage: backup-visto-docker.sh [options]

  --compose-file <path>   Compose file to operate on (default: repository compose.yaml)
  --project-name <name>   Compose project name (default: visto)
  --backup-dir <path>     Directory for the archive and its metadata (default: ./backups)
  -h, --help              Show this help

Prints "Backup created: <archive>" so the update script can pick the archive up.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --compose-file) compose_file=${2:?--compose-file needs a value}; shift 2 ;;
    --project-name) project_name=${2:?--project-name needs a value}; shift 2 ;;
    --backup-dir) backup_dir=${2:?--backup-dir needs a value}; shift 2 ;;
    -h | --help) usage; exit 0 ;;
    *) visto_docker_die "Unknown argument: $1" ;;
  esac
done

visto_docker_assert_safe_name "project name" "$project_name"
visto_docker_assert_ready "$compose_file"

mkdir -p "$backup_dir"
backup_dir=$(cd "$backup_dir" && pwd)

volume=$(visto_docker_data_volume "$compose_file" "$project_name")
utility_image=$(visto_docker_utility_image "$compose_file" "$project_name")
# Compose ps stops returning this container after stop; capture its runtime path now.
data_dir=$(visto_docker_data_dir "$compose_file" "$project_name")
visto_docker_assert_safe_name "data volume" "$volume"

archive_name="visto-data-$(visto_docker_timestamp).tar.gz"
visto_docker_assert_safe_name "archive name" "$archive_name"
archive_path="$backup_dir/$archive_name"
metadata_path="$archive_path.json"

restart_core() {
  visto_docker_compose "$compose_file" "$project_name" up -d core >/dev/null ||
    visto_docker_warn "Core could not be started after the backup. Start it manually and check health."
  visto_docker_wait_healthy "$compose_file" "$project_name" >/dev/null ||
    visto_docker_warn "Core did not report healthy after the backup. Inspect the logs before continuing."
}
trap restart_core EXIT

visto_docker_info "Stopping Core to quiesce the data volume."
visto_docker_compose "$compose_file" "$project_name" stop core >/dev/null

docker run --rm --user 0 --entrypoint tar \
  --volume "$volume:/source:ro" \
  --volume "$backup_dir:/backup" \
  "$utility_image" -czf "/backup/$archive_name" -C /source . ||
  visto_docker_die "Docker could not create the backup archive."
if [ ! -f "$archive_path" ]; then
  visto_docker_die "Docker could not create the backup archive."
fi

checksum=$(visto_docker_sha256 "$archive_path")
visto_docker_write_backup_metadata \
  "$metadata_path" "$archive_name" "$checksum" \
  "$compose_file" "$project_name" "$volume" "$utility_image" \
  "$data_dir"

visto_docker_info "Backup created: $archive_path"
visto_docker_info "Metadata: $metadata_path"
