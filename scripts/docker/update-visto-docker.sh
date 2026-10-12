#!/usr/bin/env bash
# update a Visto Docker deployment to pinned image digests.
#
# Only immutable "image@sha256:<64 hex>" references are accepted. A mutable tag
# would let the meaning of a release change after it was reviewed, and the
# Owner page only ever shows digests taken from the signed release manifest.
#
# Order: back up -> pull -> recreate -> wait for health. If any step after the
# backup fails, the pre-update data and the previously running images are
# restored automatically.

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repository_root=$(cd "$script_dir/../.." && pwd)
# shellcheck source=visto-docker.sh
. "$script_dir/visto-docker.sh"

compose_file="$repository_root/compose.yaml"
project_name="visto"
backup_dir="$(pwd)/backups"
core_image=""
web_image=""
health_timeout=$VISTO_DOCKER_HEALTH_TIMEOUT_DEFAULT

usage() {
  cat <<'USAGE'
Usage: update-visto-docker.sh --core-image <ref> --web-image <ref> [options]

  --core-image <ref>   Core image as name@sha256:<64 hex>
  --web-image <ref>    Web image as name@sha256:<64 hex>
  --compose-file <path>  Compose file to operate on (default: repository compose.yaml)
  --project-name <name>  Compose project name (default: visto)
  --backup-dir <path>    Backup directory (default: ./backups)
  --health-timeout <s>   Seconds to wait for Core health (default: 90)
  -h, --help             Show this help

Mutable tags are rejected. Copy both digests from the signed release manifest.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --core-image) core_image=${2:?--core-image needs a value}; shift 2 ;;
    --web-image) web_image=${2:?--web-image needs a value}; shift 2 ;;
    --compose-file) compose_file=${2:?--compose-file needs a value}; shift 2 ;;
    --project-name) project_name=${2:?--project-name needs a value}; shift 2 ;;
    --backup-dir) backup_dir=${2:?--backup-dir needs a value}; shift 2 ;;
    --health-timeout) health_timeout=${2:?--health-timeout needs a value}; shift 2 ;;
    -h | --help) usage; exit 0 ;;
    *) visto_docker_die "Unknown argument: $1" ;;
  esac
done

for image in "$core_image" "$web_image"; do
  if ! printf '%s' "$image" | grep -Eq '@sha256:[a-fA-F0-9]{64}$'; then
    visto_docker_die "Docker updates require immutable image references with an @sha256 digest: ${image:-<empty>}"
  fi
done
if ! printf '%s' "$health_timeout" | grep -Eq '^[0-9]+$' || [ "$health_timeout" -le 0 ]; then
  visto_docker_die "--health-timeout must be a positive integer."
fi
visto_docker_assert_safe_name "project name" "$project_name"
visto_docker_assert_ready "$compose_file"

# Restore the caller's environment on exit; the pinned images only belong to
# this process.
previous_core_setting=${VISTO_CORE_IMAGE-}
previous_web_setting=${VISTO_WEB_IMAGE-}
restore_environment() {
  if [ -n "$previous_core_setting" ]; then
    export VISTO_CORE_IMAGE="$previous_core_setting"
  else
    unset VISTO_CORE_IMAGE
  fi
  if [ -n "$previous_web_setting" ]; then
    export VISTO_WEB_IMAGE="$previous_web_setting"
  else
    unset VISTO_WEB_IMAGE
  fi
}
trap restore_environment EXIT

backup_output=$("$script_dir/backup-visto-docker.sh" \
  --compose-file "$compose_file" \
  --project-name "$project_name" \
  --backup-dir "$backup_dir") ||
  visto_docker_die "Update was not started because the pre-update backup failed."
printf '%s\n' "$backup_output"
backup_file=$(printf '%s\n' "$backup_output" | sed -n 's/^Backup created: //p' | awk 'NF { print; exit }')
if [ -z "$backup_file" ] || [ ! -f "$backup_file" ]; then
  visto_docker_die "The pre-update backup did not return its archive path."
fi

core_container=$(visto_docker_service_id "$compose_file" "$project_name" core)
web_container=$(visto_docker_service_id "$compose_file" "$project_name" web)
if [ -z "$core_container" ] || [ -z "$web_container" ]; then
  visto_docker_die "Both the Core and Web containers must be running before an update."
fi
previous_core_image=$(docker inspect "$core_container" --format '{{.Image}}')
previous_web_image=$(docker inspect "$web_container" --format '{{.Image}}')
visto_docker_info "Current images: core $previous_core_image, web $previous_web_image"

if ! docker pull "$core_image"; then
  visto_docker_die "Could not download the Core image $core_image. Nothing was changed."
fi
if ! docker pull "$web_image"; then
  visto_docker_die "Could not download the Web image $web_image. Nothing was changed."
fi

export VISTO_CORE_IMAGE="$core_image"
export VISTO_WEB_IMAGE="$web_image"

# A failed recreate has to roll back exactly like a failed health check, so both
# are covered by the same recovery branch below.
recreate_stack() {
  visto_docker_compose "$compose_file" "$project_name" up -d --no-build >/dev/null || return 1
  visto_docker_wait_healthy "$compose_file" "$project_name" "$health_timeout"
}

visto_docker_info "Recreating the stack with the pinned images."
if recreate_stack; then
  visto_docker_info "Update completed. Core image: $core_image; Web image: $web_image"
  visto_docker_info "Pre-update backup: $backup_file"
  exit 0
fi

visto_docker_warn "Update health check failed. Restoring the pre-update data and prior images."
export VISTO_CORE_IMAGE="$previous_core_image"
export VISTO_WEB_IMAGE="$previous_web_image"
if "$script_dir/restore-visto-docker.sh" \
  --backup-file "$backup_file" \
  --compose-file "$compose_file" \
  --project-name "$project_name" \
  --confirm-restore; then
  visto_docker_die "Update failed; the pre-update data and prior images were restored."
fi
visto_docker_die "Update failed and automatic restore also failed. Recover manually from $backup_file."
