#!/usr/bin/env bash
#
# Install the native Visto Server and register its launchd service.
#
# This is the macOS Server install entry point. It lays out a release under the
# shared directory contract (see visto-server-env.sh),
# writes the environment file, generates the LaunchDaemon and starts it.
#
# The install is offline and local: it does not download anything, does not
# publish, and does not touch the stable update manifest.
#
# Usage:
#   sudo bash scripts/server-unix/install-visto-server.sh [options]
#
# Options:
#   --version V         release version to install (default: 1.0.0-rc.1)
#   --source-dir DIR    directory holding bin/visto-core, bin/visto-server,
#                       web/ and scripts/ (default: the extracted package root)
#   --address ADDR      listen address (default 127.0.0.1:8787)
#   --data-dir DIR      data directory (default: the frozen macOS default)
#   --skip-start        install and register but do not start the service
#
# The directory prefix, service label and plist path honour VISTO_PREFIX,
# VISTO_SERVICE_NAME and VISTO_LAUNCHD_PLIST so administrators can run isolated
# instances without touching another deployment.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=/dev/null
. "${SCRIPT_DIR}/visto-server-env.sh"

VERSION="1.0.0-rc.1"
SOURCE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd -P)"
ADDRESS=""
DATA_DIR=""
WEB_DIR=""
SKIP_START=0

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --source-dir) SOURCE_DIR="$2"; shift 2 ;;
    --address) ADDRESS="$2"; shift 2 ;;
    --data-dir) DATA_DIR="$2"; shift 2 ;;
    --web-dir) WEB_DIR="$2"; shift 2 ;;
    --skip-start) SKIP_START=1; shift ;;
    -h | --help) sed -n '2,26p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) visto_unix_error "unknown option: $1"; exit 2 ;;
  esac
done

visto_unix_detect_platform
visto_unix_resolve_paths
visto_unix_require_root
visto_unix_assert_version "$VERSION"

[ "$VISTO_UNIX_OS" = macos ] || visto_unix_die "this entry point currently supports macOS only"

[ -x "${SOURCE_DIR}/bin/visto-core" ] || visto_unix_die "missing ${SOURCE_DIR}/bin/visto-core"
[ -x "${SOURCE_DIR}/bin/visto-server" ] || visto_unix_die "missing ${SOURCE_DIR}/bin/visto-server"

# Native Server packages require the Studio SPA, just like signed updates.
: "${WEB_DIR:=${SOURCE_DIR}/web}"
[ -f "${WEB_DIR}/index.html" ] || visto_unix_die "missing ${WEB_DIR}/index.html"

# The package contract ships the host scripts and the update path runs them
# from <prefix>/current/scripts/. An install without them cannot be updated.
[ -f "${SOURCE_DIR}/scripts/update-visto-server.sh" ] || visto_unix_die "missing ${SOURCE_DIR}/scripts/update-visto-server.sh"

for declaration in LICENSE.txt NOTICE.txt THIRD_PARTY_NOTICES.md THIRD_PARTY.spdx.json; do
  [ -s "${SOURCE_DIR}/${declaration}" ] || visto_unix_die "missing ${SOURCE_DIR}/${declaration}"
done
[ -s "${SOURCE_DIR}/UPDATE_ROOT_PUBLIC_KEY.txt" ] || visto_unix_die "missing ${SOURCE_DIR}/UPDATE_ROOT_PUBLIC_KEY.txt"
UPDATE_ROOT_PUBLIC_KEY="$(tr -d '[:space:]' < "${SOURCE_DIR}/UPDATE_ROOT_PUBLIC_KEY.txt")"
printf '%s' "$UPDATE_ROOT_PUBLIC_KEY" | grep -Eq '^[A-Za-z0-9+/]{43}=$' \
  || visto_unix_die "UPDATE_ROOT_PUBLIC_KEY.txt does not contain a base64 Ed25519 public key"

: "${VISTO_SERVER_UPDATE_SOURCES:=https://visto-server-updates.pages.dev/server/stable}"
export VISTO_SERVER_UPDATE_SOURCES

: "${ADDRESS:=127.0.0.1:8787}"
: "${DATA_DIR:=$VISTO_DATA_DIR_DEFAULT}"
# The service must use the same media runtime root as the CLI that selects it,
# otherwise the launchd job falls back to the shared default root and an
# isolated instance could read or populate another deployment's runtime.
: "${VISTO_MEDIA_RUNTIME_ROOT:=${VISTO_PREFIX}/media-runtime}"
export VISTO_MEDIA_RUNTIME_ROOT

RELEASE_DIR="${VISTO_RELEASES_DIR}/${VERSION}"
if [ -e "$RELEASE_DIR" ]; then
  visto_unix_die "release ${VERSION} already exists at ${RELEASE_DIR}"
fi

visto_unix_info "installing Visto Server ${VERSION}"
visto_unix_info "  prefix:  ${VISTO_PREFIX}"
visto_unix_info "  data:    ${DATA_DIR}"
visto_unix_info "  logs:    ${VISTO_LOG_DIR}"
visto_unix_info "  service: ${VISTO_SERVICE_NAME}"

# --- Layout -----------------------------------------------------------------
install -d -m 0755 "$RELEASE_DIR/bin"
install -d -m 0755 "$VISTO_CONFIG_DIR"
install -d -m 0755 "$VISTO_LOG_DIR"
install -d -m 0755 "$DATA_DIR"
install -d -m 0755 "$VISTO_BACKUP_DIR"
install -d -m 0755 "$VISTO_MEDIA_RUNTIME_ROOT"

install -m 0755 "${SOURCE_DIR}/bin/visto-core" "${RELEASE_DIR}/bin/visto-core"
install -m 0755 "${SOURCE_DIR}/bin/visto-server" "${RELEASE_DIR}/bin/visto-server"
install -d -m 0755 "${RELEASE_DIR}/scripts"
/bin/cp -R "${SOURCE_DIR}/scripts/." "${RELEASE_DIR}/scripts/"
[ -x "${RELEASE_DIR}/scripts/update-visto-server.sh" ] \
  || visto_unix_die "host scripts failed to copy into ${RELEASE_DIR}/scripts"
install -d -m 0755 "${RELEASE_DIR}/web"
# Copy the built SPA verbatim; it is a directory of static assets.  BSD
# install(1) on macOS has no GNU -D (create parents) and rejects it with
# "target directory ... does not exist"; cp -R preserves the tree and modes.
/bin/cp -R "${WEB_DIR}/." "${RELEASE_DIR}/web/"
[ -f "${RELEASE_DIR}/web/index.html" ] \
  || visto_unix_die "Studio web assets failed to copy into ${RELEASE_DIR}/web"
WEB_INSTALL_PATH="${VISTO_CURRENT_LINK}/web"

for declaration in LICENSE.txt NOTICE.txt THIRD_PARTY_NOTICES.md THIRD_PARTY.spdx.json; do
  install -m 0644 "${SOURCE_DIR}/${declaration}" "${RELEASE_DIR}/${declaration}"
done
install -m 0644 "${SOURCE_DIR}/UPDATE_ROOT_PUBLIC_KEY.txt" "${RELEASE_DIR}/UPDATE_ROOT_PUBLIC_KEY.txt"

# --- Environment ------------------------------------------------------------
# The host management token is a local capability token for the bundled CLI.
# It is generated once per install and reused across restarts and updates.
TOKEN_FILE="${VISTO_CONFIG_DIR}/host-management-token"
if [ -s "$TOKEN_FILE" ]; then
  HOST_TOKEN="$(cat "$TOKEN_FILE")"
else
  HOST_TOKEN="$(/usr/bin/uuidgen)"
  printf '%s' "$HOST_TOKEN" > "$TOKEN_FILE"
fi
chmod 0600 "$TOKEN_FILE"
chown root:wheel "$TOKEN_FILE"

cat > "$VISTO_ENV_FILE" <<EOF
REVIEW_STUDIO_ADDR=${ADDRESS}
REVIEW_STUDIO_DATA_DIR=${DATA_DIR}
VISTO_HOST_MANAGEMENT_TOKEN=${HOST_TOKEN}
VISTO_MEDIA_RUNTIME_ROOT=${VISTO_MEDIA_RUNTIME_ROOT}
VISTO_SERVER_UPDATE_SOURCES=${VISTO_SERVER_UPDATE_SOURCES}
VISTO_SERVER_UPDATE_ROOT_PUBLIC_KEY=${UPDATE_ROOT_PUBLIC_KEY}
EOF
printf 'REVIEW_STUDIO_WEB_DIR=%s\n' "$WEB_INSTALL_PATH" >> "$VISTO_ENV_FILE"
chmod 0600 "$VISTO_ENV_FILE"
chown root:wheel "$VISTO_ENV_FILE"
visto_unix_info "wrote ${VISTO_ENV_FILE}"

# --- LaunchDaemon -----------------------------------------------------------
# RunAtLoad starts the service when launchd loads it (boot, or bootstrap here);
# KeepAlive restarts it if it exits. Both together are what makes the server
# come back after a reboot and after an unexpected exit.
cat > "$VISTO_LAUNCHD_PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${VISTO_SERVICE_NAME}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${VISTO_CURRENT_LINK}/bin/visto-core</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>REVIEW_STUDIO_ADDR</key>
    <string>${ADDRESS}</string>
    <key>REVIEW_STUDIO_DATA_DIR</key>
    <string>${DATA_DIR}</string>
    <key>VISTO_HOST_MANAGEMENT_TOKEN</key>
    <string>${HOST_TOKEN}</string>
    <key>VISTO_MEDIA_RUNTIME_ROOT</key>
    <string>${VISTO_MEDIA_RUNTIME_ROOT}</string>
    <key>VISTO_SERVER_UPDATE_SOURCES</key>
    <string>${VISTO_SERVER_UPDATE_SOURCES}</string>
    <key>VISTO_SERVER_UPDATE_ROOT_PUBLIC_KEY</key>
    <string>${UPDATE_ROOT_PUBLIC_KEY}</string>
EOF
  cat >> "$VISTO_LAUNCHD_PLIST" <<EOF
    <key>REVIEW_STUDIO_WEB_DIR</key>
    <string>${WEB_INSTALL_PATH}</string>
EOF
cat >> "$VISTO_LAUNCHD_PLIST" <<EOF
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${VISTO_LOG_DIR}/server.log</string>
  <key>StandardErrorPath</key>
  <string>${VISTO_LOG_DIR}/server.err</string>
</dict>
</plist>
EOF
chmod 0644 "$VISTO_LAUNCHD_PLIST"
chown root:wheel "$VISTO_LAUNCHD_PLIST"
visto_unix_info "wrote ${VISTO_LAUNCHD_PLIST}"

# --- Activate ---------------------------------------------------------------
visto_unix_switch_release "$RELEASE_DIR"
visto_unix_info "activated ${VISTO_CURRENT_LINK} -> ${RELEASE_DIR}"

if [ "$SKIP_START" = "1" ]; then
  visto_unix_info "skip-start requested; service is registered but not started"
  exit 0
fi

visto_unix_service_start
visto_unix_info "service started"
