#!/usr/bin/env bash
# Shared helpers for the native Linux and macOS Server update path.
#
# Source this file; do not execute it. It defines the directory contract, the
# service abstraction and the archive guards shared by the backup, restore,
# update and rollback scripts.
#
# This file is the single implementation of the native package directory and
# artifact naming contract used by install, update, backup and restore tools.

set -euo pipefail

VISTO_UNIX_KEEP_RELEASES_DEFAULT=3
VISTO_UNIX_MAX_ENTRY_COUNT_DEFAULT=100000
VISTO_UNIX_MAX_EXPANDED_BYTES_DEFAULT=$((100 * 1024 * 1024 * 1024))
VISTO_UNIX_HEALTH_TIMEOUT_DEFAULT=90
# How long a service stop may take to become provable. Callers that quiesce,
# replace or switch data abort when the stop is not confirmed within this time.
VISTO_UNIX_SERVICE_STOP_TIMEOUT_DEFAULT=30

# Top-level entries allowed inside a native Server package. bin/ carries the
# verifier and Core, web/ the Studio bundle, scripts/ the host scripts,
# runtime/ a Visto-managed media runtime and docs/ the shipped notices.
VISTO_UNIX_ALLOWED_PREFIXES="bin/ web/ scripts/ runtime/ docs/"
VISTO_UNIX_ALLOWED_ROOT_FILES="
install.sh
uninstall.sh
visto-server.json
visto.env
LICENSE.txt
NOTICE.txt
THIRD_PARTY_NOTICES.md
FFMPEG_DISTRIBUTION.md
THIRD_PARTY_DISTRIBUTION_INVENTORY.md
THIRD_PARTY.spdx.json
UPDATE_ROOT_PUBLIC_KEY.txt
"

visto_unix_error() { printf 'ERROR: %s\n' "$*" >&2; }
visto_unix_die() {
  visto_unix_error "$*"
  exit 1
}
# Restore path rejections are a distinct outcome from a broken script: the
# input was understood, but the restore must not proceed and nothing was
# changed. Exit code 11 mirrors visto-server backup precheck.
visto_unix_reject() {
  visto_unix_error "$*"
  exit 11
}
visto_unix_info() { printf '%s\n' "$*"; }
visto_unix_warn() { printf 'WARNING: %s\n' "$*" >&2; }

# Rejection reason shared with the precheck command so a legacy archive is
# refused with the same explanation everywhere.
visto_unix_legacy_restore_rejection() {
  cat <<'LEGACY'
Backup metadata uses the legacy format (schemaVersion 1) without a source data
directory, so the restore cannot prove it would land in the original data
directory. Current data was not changed. A fresh backup is possible only if the
original data still exists and is readable. If only this legacy archive remains,
preserve it and its metadata: this release has no supported recovery or conversion
path for that archive. Do not relabel it as v2 or substitute the current directory.
LEGACY
}

visto_unix_sha256() {
  local file=$1
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{ sub(/^\\/, "", $1); print $1 }'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{ sub(/^\\/, "", $1); print $1 }'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 -r "$file" | awk '{ print $1 }'
  else
    visto_unix_die "No SHA-256 utility found. Install sha256sum, shasum or openssl."
  fi
}

visto_unix_timestamp() { date -u +%Y%m%d-%H%M%S; }
visto_unix_iso_timestamp() { date -u +%Y-%m-%dT%H:%M:%SZ; }

visto_unix_assert_safe_name() {
  local label=$1 value=$2
  if ! printf '%s' "$value" | grep -Eq '^[A-Za-z0-9._-]+$'; then
    visto_unix_die "$label must only contain letters, digits, dot, dash and underscore: $value"
  fi
}

# Only these characters may appear in a release version: the value becomes a
# directory name and is switched by symlink.
visto_unix_assert_version() {
  local value=$1
  if ! printf '%s' "$value" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
    visto_unix_die "Release version must be a SemVer value: $value"
  fi
}

# ---------------------------------------------------------------------------
# Platform detection
# ---------------------------------------------------------------------------

visto_unix_detect_platform() {
  local kernel machine
  kernel=$(uname -s)
  machine=$(uname -m)
  case "$kernel" in
    Linux) VISTO_UNIX_OS=linux ;;
    Darwin) VISTO_UNIX_OS=macos ;;
    *) visto_unix_die "Unsupported operating system for the native Server package: $kernel" ;;
  esac
  case "$machine" in
    x86_64 | amd64) VISTO_UNIX_ARCH=amd64 ;;
    aarch64 | arm64) VISTO_UNIX_ARCH=arm64 ;;
    *) visto_unix_die "Unsupported architecture for the native Server package: $machine" ;;
  esac
  VISTO_UNIX_PLATFORM="${VISTO_UNIX_OS}-${VISTO_UNIX_ARCH}"
  # Manifest artifact selectors shared with signed update metadata.
  VISTO_UNIX_ARTIFACT_KIND="${VISTO_UNIX_OS}-server"
}

# ---------------------------------------------------------------------------
# Directory contract
# ---------------------------------------------------------------------------

visto_unix_resolve_paths() {
  : "${VISTO_PREFIX:=}"
  if [ -z "$VISTO_PREFIX" ]; then
    case "$VISTO_UNIX_OS" in
      linux) VISTO_PREFIX=/opt/visto ;;
      macos) VISTO_PREFIX=/Library/Visto ;;
    esac
  fi
  case "$VISTO_UNIX_OS" in
    linux)
      VISTO_RELEASES_DIR="$VISTO_PREFIX/releases"
      VISTO_CURRENT_LINK="$VISTO_PREFIX/current"
      VISTO_CONFIG_DIR=${VISTO_CONFIG_DIR:-/etc/visto}
      VISTO_ENV_FILE=${VISTO_ENV_FILE:-$VISTO_CONFIG_DIR/visto.env}
      VISTO_DATA_DIR_DEFAULT=${VISTO_DATA_DIR_DEFAULT:-/var/lib/visto}
      VISTO_BACKUP_DIR=${VISTO_BACKUP_DIR:-/var/backups/visto}
      VISTO_LOG_DIR=${VISTO_LOG_DIR:-/var/log/visto}
      VISTO_SERVICE_NAME=${VISTO_SERVICE_NAME:-visto.service}
      VISTO_LAUNCHD_PLIST=""
      ;;
    macos)
      VISTO_RELEASES_DIR="$VISTO_PREFIX/releases"
      VISTO_CURRENT_LINK="$VISTO_PREFIX/current"
      VISTO_CONFIG_DIR=${VISTO_CONFIG_DIR:-$VISTO_PREFIX/config}
      VISTO_ENV_FILE=${VISTO_ENV_FILE:-$VISTO_CONFIG_DIR/visto.env}
      # Honour an already-exported value so isolated instances can use a
      # dedicated data root instead of the shared macOS data location.
      VISTO_DATA_DIR_DEFAULT=${VISTO_DATA_DIR_DEFAULT:-"/Library/Application Support/Visto/data"}
      VISTO_BACKUP_DIR=${VISTO_BACKUP_DIR:-"/Library/Application Support/Visto/backups"}
      VISTO_LOG_DIR=${VISTO_LOG_DIR:-/Library/Logs/Visto}
      VISTO_SERVICE_NAME=${VISTO_SERVICE_NAME:-com.visto.server}
      VISTO_LAUNCHD_PLIST=${VISTO_LAUNCHD_PLIST:-/Library/LaunchDaemons/com.visto.server.plist}
      ;;
  esac
  VISTO_RECOVERY_DIR="$VISTO_PREFIX/recovery"
}

visto_unix_require_root() {
  if [ "$(id -u)" != "0" ]; then
    visto_unix_die "Run this command as root (sudo). Native Server updates write to $VISTO_PREFIX and manage the service."
  fi
}

visto_unix_read_env() {
  if [ ! -f "$VISTO_ENV_FILE" ]; then
    visto_unix_die "Visto environment file was not found: $VISTO_ENV_FILE"
  fi
  VISTO_SERVER_ADDRESS=$(visto_unix_env_value REVIEW_STUDIO_ADDR)
  VISTO_DATA_DIR=$(visto_unix_env_value REVIEW_STUDIO_DATA_DIR)
  : "${VISTO_SERVER_ADDRESS:=127.0.0.1:8787}"
  : "${VISTO_DATA_DIR:=$VISTO_DATA_DIR_DEFAULT}"
}

visto_unix_env_value() {
  local key=$1
  sed -n "s/^[[:space:]]*${key}[[:space:]]*=[[:space:]]*[\"']\{0,1\}\([^\"']*\)[\"']\{0,1\}[[:space:]]*$/\1/p" \
    "$VISTO_ENV_FILE" | awk 'NF { print; exit }'
}

visto_unix_current_release() {
  if [ ! -e "$VISTO_CURRENT_LINK" ]; then
    visto_unix_die "No active Visto release was found at $VISTO_CURRENT_LINK."
  fi
  readlink "$VISTO_CURRENT_LINK"
}

visto_unix_current_version() {
  basename "$(visto_unix_current_release)"
}

# Atomically point the current symlink at a release directory. rename(2) over
# the existing symlink is the only step that makes the switch visible, and it
# replaces the link in one operation.
visto_unix_switch_release() {
  local target=$1 temp_link
  if [ ! -d "$target" ]; then
    visto_unix_die "Release directory was not found: $target"
  fi
  if [ -e "$VISTO_CURRENT_LINK" ] && [ ! -L "$VISTO_CURRENT_LINK" ]; then
    visto_unix_die "Active release path must be a symbolic link: $VISTO_CURRENT_LINK"
  fi
  temp_link="$VISTO_CURRENT_LINK.new"
  rm -f "$temp_link"
  ln -s "$target" "$temp_link"
  # Plain mv follows a destination directory symlink instead of replacing it.
  # Use each host's system mv so a PATH override cannot change option semantics.
  case "$(uname -s)" in
    Darwin) /bin/mv -fh "$temp_link" "$VISTO_CURRENT_LINK" ;;
    Linux) /bin/mv -fT "$temp_link" "$VISTO_CURRENT_LINK" ;;
    *) visto_unix_die "Atomic release switching requires Linux or macOS." ;;
  esac
}

# Newest first, numeric per component. `sort -V` is GNU-only and macOS ships
# BSD sort, so the components are ordered explicitly.
visto_unix_list_releases() {
  [ -d "$VISTO_RELEASES_DIR" ] || return 0
  # `find -printf` is GNU-only and `sort -V` is GNU-only; macOS ships BSD
  # versions of both, so only the portable spelling is used.
  find "$VISTO_RELEASES_DIR" -mindepth 1 -maxdepth 1 -type d -exec basename {} \; |
    sort -t. -k1,1nr -k2,2nr -k3,3nr
}

visto_unix_server_binary() {
  printf '%s' "$VISTO_CURRENT_LINK/bin/visto-server"
}

visto_unix_require_installed() {
  local binary
  binary=$(visto_unix_server_binary)
  if [ ! -x "$binary" ]; then
    visto_unix_die "The installed Visto Server verifier is unavailable: $binary"
  fi
}

# ---------------------------------------------------------------------------
# Service abstraction
# ---------------------------------------------------------------------------

# A stop is reported as done only when the host service manager proves the
# service is no longer running: for macOS the launchd job must be gone AND the
# process that belonged to it must have exited; for Linux systemctl must reach
# an inactive state. Anything that cannot be proven returns non-zero. Callers
# that quiesce, replace or switch data must abort on non-zero and must not start
# another release afterwards. This helper never signals a PID itself: launchd
# and systemd own the lifecycle, and killing a PID would bypass the job.
visto_unix_service_stop() {
  local timeout=${VISTO_UNIX_SERVICE_STOP_TIMEOUT:-$VISTO_UNIX_SERVICE_STOP_TIMEOUT_DEFAULT}
  if ! printf '%s' "$timeout" | grep -Eq '^[0-9]+$'; then
    visto_unix_error "VISTO_UNIX_SERVICE_STOP_TIMEOUT must be a whole number of seconds: $timeout"
    return 1
  fi
  case "$VISTO_UNIX_OS" in
    linux) visto_unix_linux_service_stop "$timeout" ;;
    macos) visto_unix_macos_service_stop "$timeout" ;;
    *)
      visto_unix_error "Unsupported operating system for service control: ${VISTO_UNIX_OS:-unset}"
      return 1
      ;;
  esac
}

# kill -0 only probes the process table; it never delivers a signal. A non-root
# caller can get EPERM for a live process, so ps corroborates a failed probe.
visto_unix_service_pid_running() {
  local pid=${1:-}
  [ -n "$pid" ] || return 1
  if kill -0 "$pid" 2>/dev/null; then
    return 0
  fi
  [ -n "$(ps -p "$pid" -o pid= 2>/dev/null)" ]
}

# Poll the confirmation until it passes or the timeout expires. The reason for
# the last failure is left in VISTO_UNIX_STOP_REASON for the error message.
visto_unix_service_stop_wait() {
  local timeout=$1
  shift
  local deadline
  deadline=$(( $(date +%s) + timeout ))
  while true; do
    if visto_unix_service_confirm_stopped "$@"; then
      return 0
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      return 1
    fi
    sleep 1
  done
}

visto_unix_service_confirm_stopped() {
  case "$VISTO_UNIX_OS" in
    linux) visto_unix_linux_confirm_stopped ;;
    macos) visto_unix_macos_confirm_stopped "${1:-}" ;;
    *)
      VISTO_UNIX_STOP_REASON="unsupported platform ${VISTO_UNIX_OS:-unset}"
      return 1
      ;;
  esac
}

# ---------------------------------------------------------------------------
# Linux service control (systemd)
# ---------------------------------------------------------------------------

visto_unix_linux_service_state() {
  local state=""
  state=$(systemctl is-active "$VISTO_SERVICE_NAME" 2>/dev/null) || true
  printf '%s' "$(printf '%s\n' "$state" | awk 'NF { print; exit }')"
}

visto_unix_linux_confirm_stopped() {
  local state
  VISTO_UNIX_STOP_REASON=""
  state=$(visto_unix_linux_service_state)
  case "$state" in
    inactive | failed | unknown | not-found) return 0 ;;
    "") VISTO_UNIX_STOP_REASON="systemctl is-active returned no state for ${VISTO_SERVICE_NAME}" ;;
    *) VISTO_UNIX_STOP_REASON="systemctl reports ${VISTO_SERVICE_NAME} is ${state}" ;;
  esac
  return 1
}

visto_unix_linux_service_stop() {
  local timeout=$1
  if ! systemctl stop "$VISTO_SERVICE_NAME" >/dev/null 2>&1; then
    visto_unix_error "systemctl stop ${VISTO_SERVICE_NAME} failed. The service was not stopped."
    return 1
  fi
  if visto_unix_service_stop_wait "$timeout"; then
    return 0
  fi
  visto_unix_error "systemctl stop ${VISTO_SERVICE_NAME} did not reach an inactive state within ${timeout} seconds (${VISTO_UNIX_STOP_REASON:-state unknown})."
  return 1
}

# ---------------------------------------------------------------------------
# macOS service control (launchd)
# ---------------------------------------------------------------------------

# Query the launched job. Sets VISTO_UNIX_JOB_STATE to loaded, absent or unknown
# and VISTO_UNIX_JOB_PID to the job's process when it has one. `absent` means
# launchd itself reported that no such job exists; a permission or unknown error
# stays `unknown` and must never be treated as a stopped service.
visto_unix_macos_job_state() {
  local output status
  VISTO_UNIX_JOB_STATE=unknown
  VISTO_UNIX_JOB_PID=""
  VISTO_UNIX_JOB_DETAIL=""
  if output=$(launchctl print "system/$VISTO_SERVICE_NAME" 2>&1); then
    status=0
  else
    status=$?
  fi
  if [ "$status" = "0" ]; then
    VISTO_UNIX_JOB_STATE=loaded
    VISTO_UNIX_JOB_PID=$(printf '%s\n' "$output" |
      sed -n 's/^[[:space:]]*pid[[:space:]]*=[[:space:]]*\([0-9][0-9]*\)[[:space:]]*$/\1/p' |
      awk 'NF { print; exit }')
    return 0
  fi
  VISTO_UNIX_JOB_DETAIL=$output
  if printf '%s\n' "$output" | grep -Eqi 'could not find service|no such process|not find service|service not found'; then
    VISTO_UNIX_JOB_STATE=absent
    return 0
  fi
  return 1
}

visto_unix_macos_confirm_stopped() {
  local expected_pid=${1:-}
  VISTO_UNIX_STOP_REASON=""
  if ! visto_unix_macos_job_state; then
    VISTO_UNIX_STOP_REASON="launchctl print system/${VISTO_SERVICE_NAME} failed: ${VISTO_UNIX_JOB_DETAIL:-no output}"
    return 1
  fi
  if [ "$VISTO_UNIX_JOB_STATE" != "absent" ]; then
    VISTO_UNIX_STOP_REASON="the launchd job ${VISTO_SERVICE_NAME} is still loaded"
    return 1
  fi
  if visto_unix_service_pid_running "$expected_pid"; then
    VISTO_UNIX_STOP_REASON="process ${expected_pid} of ${VISTO_SERVICE_NAME} is still running"
    return 1
  fi
  return 0
}

visto_unix_macos_service_stop() {
  local timeout=$1 expected_pid
  if ! visto_unix_macos_job_state; then
    visto_unix_error "Could not read the state of ${VISTO_SERVICE_NAME} (launchctl print system/${VISTO_SERVICE_NAME}: ${VISTO_UNIX_JOB_DETAIL:-no output}). The service was not stopped."
    return 1
  fi
  if [ "$VISTO_UNIX_JOB_STATE" = "absent" ]; then
    # launchd reports no such job: nothing can be running under this label, so
    # there is no process to wait for either.
    return 0
  fi
  expected_pid=$VISTO_UNIX_JOB_PID
  if [ -n "$expected_pid" ]; then
    visto_unix_info "Stopping ${VISTO_SERVICE_NAME} (pid ${expected_pid})."
  else
    visto_unix_info "Stopping ${VISTO_SERVICE_NAME}."
  fi
  if ! launchctl bootout "system/$VISTO_SERVICE_NAME" >/dev/null 2>&1; then
    visto_unix_warn "launchctl bootout system/${VISTO_SERVICE_NAME} failed; trying launchctl unload."
    if ! launchctl unload -w "$VISTO_LAUNCHD_PLIST" >/dev/null 2>&1; then
      # Both verbs were refused. Re-check once before failing: the job may have
      # gone away on its own, and a stop is only reported as done when the job
      # and its process are both provably gone.
      if visto_unix_macos_confirm_stopped "$expected_pid"; then
        return 0
      fi
      visto_unix_error "Neither launchctl bootout nor launchctl unload could stop ${VISTO_SERVICE_NAME} (${VISTO_UNIX_STOP_REASON:-state unknown})."
      return 1
    fi
  fi
  if visto_unix_service_stop_wait "$timeout" "$expected_pid"; then
    return 0
  fi
  visto_unix_error "Timed out after ${timeout} seconds waiting for ${VISTO_SERVICE_NAME} to stop (${VISTO_UNIX_STOP_REASON:-state unknown})."
  return 1
}

visto_unix_service_start() {
  case "$VISTO_UNIX_OS" in
    linux) systemctl start "$VISTO_SERVICE_NAME" ;;
    macos)
      if [ ! -f "$VISTO_LAUNCHD_PLIST" ]; then
        visto_unix_die "LaunchDaemon was not found: $VISTO_LAUNCHD_PLIST"
      fi
      # A bootstrap directly after a bootout can hit the teardown race; retry
      # before falling back to the legacy load path.
      local attempt=0
      until launchctl bootstrap system "$VISTO_LAUNCHD_PLIST" >/dev/null 2>&1; do
        attempt=$((attempt + 1))
        [ "$attempt" -ge 3 ] && break
        sleep 2
      done
      if launchctl print "system/$VISTO_SERVICE_NAME" >/dev/null 2>&1; then
        return 0
      fi
      launchctl load -w "$VISTO_LAUNCHD_PLIST"
      ;;
  esac
}

# Returns non-zero instead of exiting so callers can roll back first.
visto_unix_wait_ready() {
  local address=${1:-$VISTO_SERVER_ADDRESS}
  local timeout=${2:-$VISTO_UNIX_HEALTH_TIMEOUT_DEFAULT}
  local host port deadline
  host=${address%:*}
  port=${address##*:}
  if [ -z "$host" ] || [ -z "$port" ] || [ "$host" = "$address" ]; then
    visto_unix_error "Invalid listen address: $address"
    return 1
  fi
  deadline=$(( $(date +%s) + timeout ))
  while true; do
    if (exec 3<>"/dev/tcp/$host/$port") 2>/dev/null; then
      exec 3>&- 2>/dev/null || true
      return 0
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      visto_unix_error "Visto Server did not become reachable at $address within ${timeout} seconds."
      return 1
    fi
    sleep 2
  done
}

# ---------------------------------------------------------------------------
# Archive guards
# ---------------------------------------------------------------------------

# Reject one tar member name. Only separators are normalised: stripping a
# leading run of '.' and '/' would rewrite "../x" into "x" and "/etc/x" into
# "etc/x" and defeat every guard below.
visto_unix_assert_archive_entry() {
  local entry=$1
  local name=${entry//\\//}
  [ -n "$name" ] || return 0
  case "$name" in
    /*) visto_unix_die "Update archive contains an unsafe path: $entry" ;;
  esac
  if [ "${name#*:}" != "$name" ]; then
    visto_unix_die "Update archive contains an unsafe path: $entry"
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
      visto_unix_die "Update archive contains an unsafe path: $entry"
    fi
  done
}

# Accepts the default listing layout of both GNU tar (the packaging tar) and
# bsdtar (the system tar on macOS): the first all-digit field from index 3 on is
# the member size, because index 2 may be a link count on bsdtar. A line with no
# parseable size is rejected instead of being counted as zero.
visto_unix_measure_archive_bytes() {
  local max_bytes=$1
  awk -v max="$max_bytes" '
    $1 !~ /^[-d]/ { print "ERR unsupported-member-type"; failed = 1; exit }
    / link to / { print "ERR hardlink-member"; failed = 1; exit }
    {
      size = ""
      for (i = 3; i <= NF; i++) {
        if ($i ~ /^[0-9]+$/) { size = $i; break }
      }
      if (size == "") { print "ERR unsupported-member-size"; failed = 1; exit }
      total += size
      if (total > max) { print "ERR expanded-size-limit"; failed = 1; exit }
    }
    END { if (!failed) printf "OK %d\n", total + 0 }
  '
}

# Refuse to start when the target filesystem cannot hold the worst-case
# extraction. This is the format-independent backstop: the listing parser above
# fails closed, but it should never be the only thing standing between a hostile
# archive and a full disk.
visto_unix_assert_free_space() {
  local target=$1 required_bytes=$2
  local available
  available=$(df -Pk "$target" | awk 'NR == 2 { print $4 }')
  if [ -z "$available" ] || [ "$available" -lt $(( required_bytes / 1024 )) ]; then
    visto_unix_die "Not enough free space at $target for a ${required_bytes} byte extraction."
  fi
}

# Expanded size of the last validated archive; used by the callers to assert
# free space against reality instead of the static worst-case limit.
VISTO_UNIX_EXPANDED_BYTES=""

visto_unix_validate_package() {
  local archive=$1
  local max_entries=${2:-$VISTO_UNIX_MAX_ENTRY_COUNT_DEFAULT}
  local max_bytes=${3:-$VISTO_UNIX_MAX_EXPANDED_BYTES_DEFAULT}
  local names listing entry detail name first_char entry_count
  local seen_names=""

  # Names come from -tzf and types/sizes from -tvzf. Parsing the member name out
  # of a verbose line is not portable: GNU tar and bsdtar use different field
  # counts. Both listings walk the archive in the same order.
  names=$(tar -tzf "$archive") || visto_unix_die "Update archive could not be read."
  listing=$(tar -tzvf "$archive") || visto_unix_die "Update archive could not be read."

  entry_count=$(printf '%s\n' "$names" | awk 'NF { count++ } END { print count + 0 }')
  if [ "$entry_count" -gt "$max_entries" ]; then
    visto_unix_die "Update archive exceeds the entry limit of ${max_entries}."
  fi
  if [ "$entry_count" -ne "$(printf '%s\n' "$listing" | awk 'NF { count++ } END { print count + 0 }')" ]; then
    visto_unix_die "Update archive listing is inconsistent; refusing to extract it."
  fi

  while IFS= read -r name <&3 && IFS= read -r detail <&4; do
    [ -n "$name" ] || continue
    # Normalise once up front: tar -C dir . names members "./bin/x", and the
    # safety guards, prefix check and required-file matching below must see
    # the same canonical name regardless of the packaging spelling.
    while [ "${name#./}" != "$name" ]; do
      name=${name#./}
    done
    [ -n "$name" ] || continue
    first_char=${detail%"${detail#?}"}
    visto_unix_assert_archive_entry "$name"
    if [ "$first_char" != "-" ] && [ "$first_char" != "d" ]; then
      visto_unix_die "Update archive contains an unsupported member type: $name"
    fi
    if printf '%s\n' "$seen_names" | grep -Fxq "$name"; then
      visto_unix_die "Update archive contains a duplicate path: $name"
    fi
    seen_names=$(printf '%s\n%s' "$seen_names" "$name")
    visto_unix_assert_allowed_package_path "$name"
  done 3<<< "$names" 4<<< "$listing"

  local result status expanded
  result=$(printf '%s\n' "$listing" | visto_unix_measure_archive_bytes "$max_bytes")
  status=${result%% *}
  if [ "$status" != "OK" ]; then
    visto_unix_die "Update archive rejected (${result#ERR })."
  fi
  expanded=${result##* }

  for required in bin/visto-server bin/visto-core web/index.html; do
    if ! printf '%s\n' "$seen_names" | grep -Fxq "$required"; then
      visto_unix_die "Update archive is missing required file: $required"
    fi
  done

  VISTO_UNIX_EXPANDED_BYTES="$expanded"
  visto_unix_info "Update archive validated: ${entry_count} entries, ${expanded} expanded bytes."
}

visto_unix_assert_allowed_package_path() {
  local name=$1 prefix segment
  local normalised=""
  # tar lists directory members with a trailing slash; keep it so "bin/" can
  # match the allowed "bin/" prefix instead of being mistaken for a root file.
  local trailing=""
  case "$name" in / | //*) visto_unix_die "Update archive contains an unsafe path: $name" ;; */) trailing=/ ;; esac
  local IFS=/
  # Normalise before the prefix check: "bin/../../../etc/passwd" starts with an
  # allowed prefix but must not be treated as one.
  for segment in $name; do
    [ -n "$segment" ] || continue
    case "$segment" in
      .) continue ;;
      ..) visto_unix_die "Update archive contains an unsafe path: $name" ;;
    esac
    normalised="${normalised:+$normalised/}$segment"
  done
  unset IFS
  [ -n "$normalised" ] || return 0
  normalised="$normalised$trailing"
  for prefix in $VISTO_UNIX_ALLOWED_PREFIXES; do
    case "$normalised" in
      "$prefix"*) return 0 ;;
    esac
  done
  case "$normalised" in
    */*) visto_unix_die "Update archive contains an unexpected path: $name" ;;
  esac
  if ! printf '%s\n' $VISTO_UNIX_ALLOWED_ROOT_FILES | grep -Fxq "$normalised"; then
    visto_unix_die "Update archive contains an unexpected file: $name"
  fi
}

# ---------------------------------------------------------------------------
# Backup metadata
# ---------------------------------------------------------------------------

visto_unix_read_metadata() {
  local metadata_path=$1 key=$2
  sed -n "s/.*\"$key\"[[:space:]]*:[[:space:]]*\"\{0,1\}\([^\",}]*\)\"\{0,1\}.*/\1/p" \
    "$metadata_path" | awk 'NF { print; exit }'
}

# The restore precheck and the metadata writer run the visto-server binary that
# ships in the same release as these scripts (never the current-link binary:
# during an update rollback the current link may point at the previous release).
# Tests override it with a stub.
visto_unix_release_binary() {
  if [ -n "${VISTO_UNIX_SERVER_BIN:-}" ]; then
    printf '%s' "$VISTO_UNIX_SERVER_BIN"
    return 0
  fi
  printf '%s/bin/visto-server' "$(cd "$script_dir/.." && pwd)"
}

visto_unix_write_backup_metadata() {
  local metadata_path=$1 archive_name=$2 checksum=$3 archive_root=$4
  local binary
  binary=$(visto_unix_release_binary)
  if [ ! -x "$binary" ]; then
    visto_unix_die "Backup failed: release verifier is unavailable; update must not continue."
  fi
  if ! "$binary" backup metadata \
    --data-dir "$VISTO_DATA_DIR" \
    --archive "$archive_name" \
    --sha256 "$checksum" \
    --output "$metadata_path"; then
    visto_unix_die "Backup failed: database facts could not be verified; archive is retained for investigation only, not a recovery point."
  fi
}

visto_unix_json_value() {
  local file=$1 key=$2
  sed -n "s/.*\"$key\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$file" | awk 'NF { print; exit }'
}

# ---------------------------------------------------------------------------
# Diagnostics
# ---------------------------------------------------------------------------

# A failed update must leave evidence behind: the step, the versions involved
# and a doctor report from whichever binary is still installed.
visto_unix_write_failure_report() {
  local report_dir=$1 step=$2 message=$3
  local report_file="$report_dir/update-failure-$(visto_unix_timestamp).log"
  mkdir -p "$report_dir"
  {
    printf 'step: %s\n' "$step"
    printf 'recordedAt: %s\n' "$(visto_unix_iso_timestamp)"
    printf 'platform: %s\n' "${VISTO_UNIX_PLATFORM:-unknown}"
    printf 'prefix: %s\n' "${VISTO_PREFIX:-unknown}"
    printf 'activeRelease: %s\n' "$(visto_unix_current_release 2>/dev/null || printf 'unknown')"
    printf 'message: %s\n' "$message"
  } >"$report_file"
  local binary
  binary="$VISTO_CURRENT_LINK/bin/visto-server"
  if [ -x "$binary" ]; then
    {
      printf '\n-- visto-server doctor --\n'
      "$binary" doctor --json 2>&1 || true
    } >>"$report_file"
  fi
  printf '%s\n' "$report_file"
}
