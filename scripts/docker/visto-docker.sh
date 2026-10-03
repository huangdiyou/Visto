#!/usr/bin/env bash
# shared helpers for the free Server Docker update path.
#
# These helpers only ever run on the deployment host, invoked by an
# administrator. The Owner web page never calls them: it only shows the
# commands that have to be run here. Source this file, do not execute it.

set -euo pipefail

VISTO_DOCKER_MAX_EXPANDED_BYTES_DEFAULT=$((100 * 1024 * 1024 * 1024))
VISTO_DOCKER_MAX_ENTRY_COUNT_DEFAULT=100000
VISTO_DOCKER_HEALTH_TIMEOUT_DEFAULT=90

visto_docker_error() {
  printf 'ERROR: %s\n' "$*" >&2
}

visto_docker_die() {
  visto_docker_error "$*"
  exit 1
}

visto_docker_info() {
  printf '%s\n' "$*"
}

visto_docker_warn() {
  printf 'WARNING: %s\n' "$*" >&2
}

# Names that end up in generated JSON, docker volume names or archive names are
# restricted to an obviously safe charset instead of being escaped after the
# fact.
visto_docker_assert_safe_name() {
  local label=$1 value=$2
  if ! printf '%s' "$value" | grep -Eq '^[A-Za-z0-9._-]+$'; then
    visto_docker_die "$label must only contain letters, digits, dot, dash and underscore: $value"
  fi
}

# GNU coreutils prefixes a line with "\" when the file name itself had to be
# escaped. A Windows path handed to a native binary through MSYS contains
# backslashes, so the prefix has to be stripped before the digest is compared.
visto_docker_sha256() {
  local file=$1
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{ sub(/^\\/, "", $1); print $1 }'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{ sub(/^\\/, "", $1); print $1 }'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 -r "$file" | awk '{ print $1 }'
  else
    visto_docker_die "No SHA-256 utility found. Install sha256sum, shasum or openssl."
  fi
}

visto_docker_timestamp() {
  date -u +%Y%m%d-%H%M%S
}

visto_docker_iso_timestamp() {
  date -u +%Y-%m-%dT%H:%M:%SZ
}

# Arguments are always forwarded as discrete words so an image reference, path
# or backup name can never be re-parsed as a docker compose flag.
visto_docker_compose() {
  local compose_file=$1 project_name=$2
  shift 2
  docker compose -f "$compose_file" -p "$project_name" "$@"
}

visto_docker_assert_ready() {
  local compose_file=$1
  command -v docker >/dev/null 2>&1 ||
    visto_docker_die "Docker Engine is required. Install Docker Engine 26+ with Compose v2 and retry."
  docker compose version >/dev/null 2>&1 ||
    visto_docker_die "Docker Compose v2 is not available."
  if [ ! -f "$compose_file" ]; then
    visto_docker_die "Compose file was not found: $compose_file"
  fi
}

visto_docker_service_id() {
  local compose_file=$1 project_name=$2 service=$3
  visto_docker_compose "$compose_file" "$project_name" ps -q "$service" |
    awk 'NF { print; exit }'
}

visto_docker_data_volume() {
  local compose_file=$1 project_name=$2
  local container_id volume
  container_id=$(visto_docker_service_id "$compose_file" "$project_name" core)
  if [ -z "$container_id" ]; then
    visto_docker_die "The Visto Core container is not available. Start the deployment before running this operation."
  fi
  volume=$(docker inspect "$container_id" --format '{{range .Mounts}}{{if and (eq .Type "volume") (eq .Destination "/var/lib/visto")}}{{.Name}}{{end}}{{end}}')
  if [ -z "$volume" ]; then
    visto_docker_die "Could not determine the Visto data volume from the Core container."
  fi
  printf '%s' "$volume"
}

# Backups reuse the image id already running as Core instead of pulling a
# mutable utility image. That keeps the operation reproducible and offline safe.
visto_docker_utility_image() {
  local compose_file=$1 project_name=$2
  local container_id image
  container_id=$(visto_docker_service_id "$compose_file" "$project_name" core)
  if [ -z "$container_id" ]; then
    visto_docker_die "The Visto Core container is not available. Start the deployment before running this operation."
  fi
  image=$(docker inspect "$container_id" --format '{{.Image}}')
  if ! printf '%s' "$image" | grep -Eq '^sha256:[a-f0-9]{64}$'; then
    visto_docker_die "Could not resolve the running Core image digest."
  fi
  printf '%s' "$image"
}

# Returns non-zero instead of exiting so callers can roll back before giving up.
visto_docker_wait_healthy() {
  local compose_file=$1 project_name=$2
  local timeout=${3:-$VISTO_DOCKER_HEALTH_TIMEOUT_DEFAULT}
  local deadline=$(( $(date +%s) + timeout ))
  local container_id status
  while true; do
    container_id=$(visto_docker_compose "$compose_file" "$project_name" ps -q core | awk 'NF { print; exit }')
    if [ -n "$container_id" ]; then
      status=$(docker inspect "$container_id" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}')
      if [ "$status" = "healthy" ]; then
        return 0
      fi
      if [ "$status" = "exited" ] || [ "$status" = "dead" ]; then
        visto_docker_error "The Core container stopped while waiting for its health check."
        return 1
      fi
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      visto_docker_error "The Core health check did not succeed within ${timeout} seconds."
      return 1
    fi
    sleep 2
  done
}

# Reject one tar member name. Only separators are normalised: stripping a
# leading run of '.' and '/' would silently rewrite "../x" into "x" and
# "/etc/x" into "etc/x" and defeat every guard below.
visto_docker_assert_archive_entry() {
  local entry=$1
  local name=${entry//\\//}
  [ -n "$name" ] || return 0
  case "$name" in
    /*)
      visto_docker_die "Backup archive contains an unsafe path: $entry"
      ;;
  esac
  if [ "${name#*:}" != "$name" ]; then
    visto_docker_die "Backup archive contains an unsafe path: $entry"
  fi
  while [ "${name#./}" != "$name" ]; do
    name=${name#./}
  done
  [ -n "$name" ] || return 0
  local segment
  local IFS=/
  for segment in $name; do
    [ -n "$segment" ] || continue
    if [ "$segment" = ".." ]; then
      visto_docker_die "Backup archive contains an unsafe path: $entry"
    fi
  done
}

# Always exits 0 and prints "OK <bytes>" or "ERR <reason>" so a caller never has
# to guess whether a parse failure means "small enough".
visto_docker_measure_archive_bytes() {
  local max_bytes=$1
  awk -v max="$max_bytes" '
    $1 !~ /^[-d]/ { print "ERR unsupported-member-type"; failed = 1; exit }
    $3 !~ /^[0-9]+$/ { print "ERR unsupported-member-size"; failed = 1; exit }
    {
      total += $3
      if (total > max) { print "ERR expanded-size-limit"; failed = 1; exit }
    }
    END { if (!failed) printf "OK %d\n", total + 0 }
  '
}

# Validates a backup archive before anything is extracted: member names, member
# types, entry count and expanded byte total. Current data is never touched.
visto_docker_validate_archive() {
  local utility_image=$1 backup_dir=$2 backup_name=$3 max_bytes=$4 max_entries=$5
  local volume_argument="${backup_dir}:/backup:ro"
  local entries entry_count entry details result status expanded

  entries=$(docker run --rm --user 0 --entrypoint tar \
    --volume "$volume_argument" "$utility_image" -tzf "/backup/$backup_name") ||
    visto_docker_die "Backup archive could not be read. Current data was not changed."

  entry_count=$(printf '%s\n' "$entries" | awk 'NF { count++ } END { print count + 0 }')
  if [ "$entry_count" -gt "$max_entries" ]; then
    visto_docker_die "Backup archive exceeds the entry limit of ${max_entries}. Current data was not changed."
  fi

  while IFS= read -r entry; do
    [ -n "$entry" ] || continue
    visto_docker_assert_archive_entry "$entry"
  done <<< "$entries"

  details=$(docker run --rm --user 0 --entrypoint tar \
    --volume "$volume_argument" "$utility_image" -tvzf "/backup/$backup_name") ||
    visto_docker_die "Backup archive details could not be read. Current data was not changed."

  result=$(printf '%s\n' "$details" | visto_docker_measure_archive_bytes "$max_bytes")
  status=${result%% *}
  if [ "$status" != "OK" ]; then
    visto_docker_die "Backup archive rejected (${result#ERR }). Current data was not changed."
  fi
  expanded=${result##* }
  visto_docker_info "Backup archive validated: ${entry_count} entries, ${expanded} expanded bytes."
}

visto_docker_read_metadata() {
  local metadata_path=$1 key=$2
  sed -n "s/.*\"$key\"[[:space:]]*:[[:space:]]*\"\{0,1\}\([^\",}]*\)\"\{0,1\}.*/\1/p" \
    "$metadata_path" | awk 'NF { print; exit }'
}

visto_docker_run_tar() {
  docker run --rm --user 0 --entrypoint tar "$@"
}

visto_docker_run_sh() {
  docker run --rm --user 0 --entrypoint sh "$@"
}

# Restore path rejections are a distinct outcome from a broken script: the
# input was understood, but the restore must not proceed and nothing was
# changed. Exit code 11 mirrors visto-server backup precheck.
visto_docker_reject() {
  visto_docker_error "$*"
  exit 11
}

# Rejection reason shared with the precheck command so a legacy archive is
# refused with the same explanation everywhere.
visto_docker_legacy_restore_rejection() {
  cat <<'LEGACY'
Backup metadata uses the legacy format (schemaVersion 1) without a source data
directory, so the restore cannot prove it would land in the original data
directory. Current data was not changed. A fresh backup is possible only if the
original data still exists and is readable. If only this legacy archive remains,
preserve it and its metadata: this release has no supported recovery or conversion
path for that archive. Do not relabel it as v2 or substitute the current directory.
LEGACY
}

visto_docker_run_server() {
  docker run --rm --user 0 --entrypoint /usr/local/bin/visto-server "$@"
}

# The container-side data directory the Core service was started with. Backup
# metadata records it and the restore precheck compares it against the target.
visto_docker_data_dir() {
  local compose_file=$1 project_name=$2
  local container_id data_dir
  container_id=$(visto_docker_service_id "$compose_file" "$project_name" core)
  if [ -z "$container_id" ]; then
    visto_docker_die "The Visto Core container is not available. Start the deployment before running this operation."
  fi
  data_dir=$(docker inspect "$container_id" --format '{{range .Config.Env}}{{println .}}{{end}}' |
    sed -n 's/^REVIEW_STUDIO_DATA_DIR=//p' | awk 'NF { print; exit }')
  if [ -z "$data_dir" ]; then
    visto_docker_die "Could not determine the data directory from the Core container environment (REVIEW_STUDIO_DATA_DIR)."
  fi
  printf '%s' "$data_dir"
}

# A recovery point requires verified database facts; failures stop the update.
visto_docker_write_backup_metadata() {
  local metadata_path=$1 archive_name=$2 checksum=$3
  local compose_file=$4 project_name=$5 volume=$6 utility_image=$7 data_dir=$8
  local metadata_name
  metadata_name=$(basename "$metadata_path")
  if visto_docker_run_server \
    --volume "$volume:$data_dir:ro" \
    --volume "$(cd "$(dirname "$metadata_path")" && pwd):/backup" \
    "$utility_image" backup metadata \
    --data-dir "$data_dir" \
    --archive "$archive_name" \
    --sha256 "$checksum" \
    --no-archive-root \
    --compose-project "$project_name" \
    --data-volume "$volume" \
    --output "/backup/$metadata_name" >/dev/null 2>&1 &&
    [ -f "$metadata_path" ]; then
    return 0
  fi
  visto_docker_die "Backup failed: database facts could not be verified; update must not continue."
}
