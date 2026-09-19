#!/usr/bin/env bash
# Update a native Linux or macOS Visto Server from a signed package.
#
# The script never downloads anything. The administrator obtains the package,
# latest.json and latest.json.sig from the same release (online or by removable
# media) and hands all three to this script.
#
# Order: stage inputs -> verify signature -> validate layout -> extract into a
# new release directory -> back up data -> stop service -> switch the current
# symlink -> start service -> wait for health. Anything after the backup fails
# rolls back to the previous release and, if the new version had already
# started, to the pre-update data backup.

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=visto-server-env.sh
. "$script_dir/visto-server-env.sh"

visto_unix_detect_platform

package_archive=""
manifest_file=""
signature_file=""
public_key=${VISTO_SERVER_UPDATE_PUBLIC_KEY:-}
keep_releases=$VISTO_UNIX_KEEP_RELEASES_DEFAULT
health_timeout=$VISTO_UNIX_HEALTH_TIMEOUT_DEFAULT
max_expanded_bytes=$VISTO_UNIX_MAX_EXPANDED_BYTES_DEFAULT
max_entry_count=$VISTO_UNIX_MAX_ENTRY_COUNT_DEFAULT

usage() {
  cat <<'USAGE'
Usage: update-visto-server.sh --package <archive.tar.gz> [options]

  --package <path>              Signed Server package for this platform
  --manifest <path>             latest.json (default: <package>.manifest.json)
  --signature <path>            latest.json.sig (default: <manifest>.sig)
  --public-key <base64>         Trusted Ed25519 release public key (or VISTO_SERVER_UPDATE_PUBLIC_KEY)
  --prefix <path>               Visto program prefix
  --config-dir <path>           Directory holding visto.env
  --keep-releases <count>       Releases to keep after a successful update (default: 3)
  --health-timeout <seconds>    Health wait after restart (default: 90)
  --max-expanded-bytes <bytes>  Expanded size limit (default: 107374182400)
  --max-entries <count>         Archive member count limit (default: 100000)
  -h, --help                    Show this help

All three release files must come from the same release. Offline updates use
exactly the same command with files copied from removable media.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --package) package_archive=${2:?--package needs a value}; shift 2 ;;
    --manifest) manifest_file=${2:?--manifest needs a value}; shift 2 ;;
    --signature) signature_file=${2:?--signature needs a value}; shift 2 ;;
    --public-key) public_key=${2:?--public-key needs a value}; shift 2 ;;
    --prefix) VISTO_PREFIX=${2:?--prefix needs a value}; shift 2 ;;
    --config-dir) VISTO_CONFIG_DIR=${2:?--config-dir needs a value}; shift 2 ;;
    --keep-releases) keep_releases=${2:?--keep-releases needs a value}; shift 2 ;;
    --health-timeout) health_timeout=${2:?--health-timeout needs a value}; shift 2 ;;
    --max-expanded-bytes) max_expanded_bytes=${2:?--max-expanded-bytes needs a value}; shift 2 ;;
    --max-entries) max_entry_count=${2:?--max-entries needs a value}; shift 2 ;;
    -h | --help) usage; exit 0 ;;
    *) visto_unix_die "Unknown argument: $1" ;;
  esac
done

for limit in "$keep_releases" "$health_timeout" "$max_expanded_bytes" "$max_entry_count"; do
  if ! printf '%s' "$limit" | grep -Eq '^[0-9]+$' || [ "$limit" -le 0 ]; then
    visto_unix_die "Numeric options must be positive integers."
  fi
done

# Validate the caller's input before touching the machine: a missing flag must
# not require root to discover.
if [ -z "$package_archive" ]; then
  visto_unix_die "--package must point at the downloaded Server package."
fi
if [ ! -f "$package_archive" ]; then
  visto_unix_die "Server package was not found: $package_archive"
fi
if [ -z "$public_key" ]; then
  visto_unix_die "A trusted Ed25519 update public key is required (--public-key or VISTO_SERVER_UPDATE_PUBLIC_KEY)."
fi
package_archive=$(cd "$(dirname "$package_archive")" && printf '%s/%s' "$(pwd)" "$(basename "$package_archive")")
[ -n "$manifest_file" ] || manifest_file="$package_archive.manifest.json"
[ -n "$signature_file" ] || signature_file="$manifest_file.sig"
for required_file in "$manifest_file" "$signature_file"; do
  if [ ! -f "$required_file" ]; then
    visto_unix_die "Required release file was not found: $required_file"
  fi
done

visto_unix_resolve_paths
visto_unix_require_root
visto_unix_read_env
visto_unix_require_installed

mkdir -p "$VISTO_RELEASES_DIR" "$VISTO_LOG_DIR" "$VISTO_RECOVERY_DIR"
chmod 755 "$VISTO_RELEASES_DIR"
chmod 700 "$VISTO_RECOVERY_DIR"

# Bind the caller-supplied package, manifest and signature to one immutable
# private copy before verification, so the archive cannot be swapped between
# signature verification, layout validation and extraction.
input_staging="$VISTO_RECOVERY_DIR/update-input-$(visto_unix_timestamp)-$$"
mkdir -p "$input_staging"
chmod 700 "$input_staging"
staged_package="$input_staging/$(basename "$package_archive")"
staged_manifest="$input_staging/$(basename "$manifest_file")"
staged_signature="$input_staging/$(basename "$signature_file")"
cp "$package_archive" "$staged_package"
cp "$manifest_file" "$staged_manifest"
cp "$signature_file" "$staged_signature"
chmod 700 "$staged_package" "$staged_manifest" "$staged_signature"

verifier="$VISTO_CURRENT_LINK/bin/visto-server"
visto_unix_info "Verifying the signed manifest against the installed verifier."
"$verifier" update verify-package \
  --manifest "$staged_manifest" \
  --signature "$staged_signature" \
  --artifact "$staged_package" \
  --kind "$VISTO_UNIX_ARTIFACT_KIND" \
  --platform "$VISTO_UNIX_PLATFORM" \
  --public-key "$public_key" >/dev/null ||
  visto_unix_die "The update package did not pass signed manifest verification."

new_version=$(visto_unix_json_value "$staged_manifest" version)
if [ -z "$new_version" ]; then
  visto_unix_die "The signed manifest does not declare a version."
fi
visto_unix_assert_version "$new_version"

current_version=$(visto_unix_current_version)
previous_release=$(visto_unix_current_release)
if [ "$new_version" = "$current_version" ]; then
  visto_unix_die "Version ${new_version} is already installed. Updates do not reinstall the same release."
fi

visto_unix_validate_package "$staged_package" "$max_entry_count" "$max_expanded_bytes"
# The static limit is a rejection cap; the space assertion uses the measured
# expanded size so a small package cannot be blocked by a large default.
space_required=$(( VISTO_UNIX_EXPANDED_BYTES * 2 ))
[ "$space_required" -lt 268435456 ] && space_required=268435456
visto_unix_assert_free_space "$VISTO_RELEASES_DIR" "$space_required"

release_dir="$VISTO_RELEASES_DIR/$new_version"
if [ -e "$release_dir" ]; then
  visto_unix_die "Release ${new_version} already exists at ${release_dir}. Releases are immutable; roll back or pick another version."
fi

staging_release="$VISTO_RELEASES_DIR/.staging-$(visto_unix_timestamp)-$$"
mkdir -p "$staging_release"
chmod 700 "$staging_release"

cleanup_staging() {
  rm -rf "$staging_release"
  rm -rf "$input_staging"
}
trap cleanup_staging EXIT

visto_unix_info "Extracting ${new_version} into a new release directory."
tar -xzf "$staged_package" -C "$staging_release" ||
  visto_unix_die "Could not extract the update package."
extracted_bytes=$(du -sk "$staging_release" | awk '{ print $1 * 1024 }')
if [ "$extracted_bytes" -gt "$max_expanded_bytes" ]; then
  visto_unix_die "The extracted release exceeds the configured limit of ${max_expanded_bytes} bytes."
fi
chmod 755 "$staging_release"
mv "$staging_release" "$release_dir"

data_backup=""
new_version_started=0

# A failure after the new release has been made current must bring back the
# previous release and leave evidence behind.
rollback() {
  local step=$1 message=$2 report
  visto_unix_warn "Update failed at ${step}. Rolling back to ${current_version}."
  report=$(visto_unix_write_failure_report "$VISTO_RECOVERY_DIR" "$step" "$message")
  # The rollback switches the current release and may replace data, so the stop
  # must be confirmed first. When it is not, nothing is switched and nothing is
  # started: the data, the current release and the pre-update backup stay as
  # they are for a manual recovery.
  if ! visto_unix_service_stop; then
    visto_unix_die "Update failed at ${step} and ${VISTO_SERVICE_NAME} could not be stopped, so no rollback was attempted. The active release and the data were left unchanged; the pre-update backup at ${data_backup:-unavailable} is preserved. Diagnostic report: ${report}"
  fi
  visto_unix_switch_release "$previous_release" || true
  if [ "$new_version_started" = "1" ] && [ -n "$data_backup" ] && [ -f "$data_backup" ]; then
    visto_unix_warn "Restoring the pre-update data backup because ${new_version} had already started."
    "$script_dir/restore-visto-server.sh" \
      --backup-file "$data_backup" \
      --prefix "$VISTO_PREFIX" \
      --config-dir "$VISTO_CONFIG_DIR" \
      --confirm-restore >/dev/null 2>&1 ||
      visto_unix_die "Automatic data restore failed; previous version remains stopped. Recover manually from ${data_backup}. Diagnostic report: ${report}"
  else
    visto_unix_service_start >/dev/null 2>&1 || true
  fi
  if visto_unix_wait_ready "$VISTO_SERVER_ADDRESS" "$health_timeout"; then
    visto_unix_die "Update failed and rolled back to ${current_version}. Diagnostic report: ${report}"
  fi
  visto_unix_die "Update failed; the previous release is active but Visto Server is not answering. Diagnostic report: ${report}"
}

visto_unix_info "Backing up the data directory before switching releases."
backup_output=$("$script_dir/backup-visto-server.sh" \
  --backup-dir "$VISTO_BACKUP_DIR" \
  --prefix "$VISTO_PREFIX" \
  --config-dir "$VISTO_CONFIG_DIR") ||
  visto_unix_die "Update was not started because the pre-update backup failed."
printf '%s\n' "$backup_output"
data_backup=$(printf '%s\n' "$backup_output" | sed -n 's/^Backup created: //p' | awk 'NF { print; exit }')
if [ -z "$data_backup" ] || [ ! -f "$data_backup" ]; then
  visto_unix_die "The pre-update backup did not return its archive path."
fi

visto_unix_info "Stopping ${VISTO_SERVICE_NAME}."
# The switch and the start after it must never run on an unconfirmed stop: a
# still-running old process would keep writing to the data directory while the
# new release is made current.
if ! visto_unix_service_stop; then
  visto_unix_die "Update was not started because ${VISTO_SERVICE_NAME} could not be stopped. No release was switched and the pre-update backup at ${data_backup} is preserved."
fi

if ! visto_unix_switch_release "$release_dir"; then
  rollback "switch" "Could not point ${VISTO_CURRENT_LINK} at ${release_dir}."
fi

visto_unix_info "Starting ${new_version}."
# Even a failed service-start call may have briefly launched and written data.
new_version_started=1
if ! visto_unix_service_start; then
  rollback "start" "The service did not start after switching to ${new_version}."
fi

if ! visto_unix_wait_ready "$VISTO_SERVER_ADDRESS" "$health_timeout"; then
  rollback "health" "Visto Server did not answer on ${VISTO_SERVER_ADDRESS} within ${health_timeout} seconds."
fi

visto_unix_info "Update completed: ${current_version} -> ${new_version}."
visto_unix_info "Previous release kept at ${previous_release}."
visto_unix_info "Pre-update backup: ${data_backup}"

pruned=0
for old in $(visto_unix_list_releases | tail -n +$((keep_releases + 1))); do
  case "$old" in
    "$new_version" | "$current_version") continue ;;
  esac
  rm -rf "$VISTO_RELEASES_DIR/$old"
  pruned=$((pruned + 1))
done
if [ "$pruned" -gt 0 ]; then
  visto_unix_info "Pruned ${pruned} old release(s); kept ${keep_releases}."
fi

visto_unix_info "Roll back with: sudo ${script_dir}/rollback-visto-server.sh --to-version ${current_version}"
