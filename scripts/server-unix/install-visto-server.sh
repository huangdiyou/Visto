#!/usr/bin/env bash
#
# Install the native Visto Server and register its service.
#
# Works on both supported platforms: it lays out a release under the shared
# directory contract (see visto-server-env.sh), writes the environment file, then
# registers and starts the service through launchd on macOS or systemd on Linux.
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
#   --data-dir DIR      data directory (default: the platform default)
#   --skip-start        install and register but do not start the service
#
# The directory prefix, service name, plist path and systemd unit path honour
# VISTO_PREFIX, VISTO_SERVICE_NAME, VISTO_LAUNCHD_PLIST and VISTO_SYSTEMD_UNIT so
# administrators can run isolated instances without touching another deployment.
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
# The free tier update channel is unsigned (docs/FREE_TIER_BOUNDARY_DESIGN.md):
# the package ships no root public key and the installer does not require one.
# The update source below stays pinned to the official host.

: "${VISTO_SERVER_UPDATE_SOURCES:=https://visto-server-updates.pages.dev/server/stable}"
export VISTO_SERVER_UPDATE_SOURCES

: "${ADDRESS:=127.0.0.1:8787}"
: "${DATA_DIR:=$VISTO_DATA_DIR_DEFAULT}"
# The service must use the same media runtime root as the CLI that selects it,
# otherwise the launchd job falls back to the shared default root and an
# isolated instance could read or populate another deployment's runtime.
if [ -z "${VISTO_MEDIA_RUNTIME_ROOT:-}" ]; then
  if [ "$VISTO_UNIX_OS" = macos ] && [ "$VISTO_PREFIX" = /Library/Visto ]; then
    # Match delivery.DefaultLayout: host CLI and launchd must select the same root.
    VISTO_MEDIA_RUNTIME_ROOT="/Library/Application Support/Visto/runtime"
  elif [ "$VISTO_UNIX_OS" = linux ] && [ "$VISTO_PREFIX" = /opt/visto ]; then
    # Match delivery.DefaultLayout and the standalone host CLI.
    VISTO_MEDIA_RUNTIME_ROOT="/var/lib/visto-runtime"
  else
    VISTO_MEDIA_RUNTIME_ROOT="${VISTO_PREFIX}/media-runtime"
  fi
fi
export VISTO_MEDIA_RUNTIME_ROOT

# append: was introduced in systemd 240. Preserve the promised 239 baseline
# by using its journal, without a privileged shell or writable log-name redirects.
if [ "$VISTO_UNIX_OS" = linux ]; then
  systemd_version=$(systemctl --version 2>/dev/null | awk 'NR == 1 && $1 == "systemd" { print $2 }')
  case "$systemd_version" in
    "" | *[!0-9]*) visto_unix_die "could not determine the systemd version" ;;
  esac
  [ "$systemd_version" -ge 239 ] || visto_unix_die "systemd 239 or newer is required"
  VISTO_SYSTEMD_STDOUT=journal
  VISTO_SYSTEMD_STDERR=journal
  if [ "$systemd_version" -ge 240 ]; then
    VISTO_SYSTEMD_STDOUT="append:${VISTO_LOG_DIR}/server.log"
    VISTO_SYSTEMD_STDERR="append:${VISTO_LOG_DIR}/server.err"
  fi
fi

RELEASE_DIR="${VISTO_RELEASES_DIR}/${VERSION}"
if [ -e "$RELEASE_DIR" ]; then
  visto_unix_die "release ${VERSION} already exists at ${RELEASE_DIR}"
fi

visto_unix_info "installing Visto Server ${VERSION}"
visto_unix_info "  prefix:  ${VISTO_PREFIX}"
visto_unix_info "  data:    ${DATA_DIR}"
visto_unix_info "  logs:    ${VISTO_LOG_DIR}"
visto_unix_info "  service: ${VISTO_SERVICE_NAME}"

# Refuse an address that is already taken before starting anything.
#
# The readiness helper used to be the only check, and it only proves that
# *something* accepts a connection on the address. With a foreign process holding
# the port that check succeeds while our own service fails to bind, so a conflict
# was reported as a healthy install and the operator only found out from the logs.
# Failing here is also better than systemd's Restart=on-failure loop, which would
# retry a bind that cannot succeed until someone intervenes.
preflight_host=${ADDRESS%:*}
preflight_port=${ADDRESS##*:}
if [ -n "$preflight_host" ] && [ -n "$preflight_port" ] && [ "$preflight_host" != "$ADDRESS" ]; then
  if (exec 3<>"/dev/tcp/${preflight_host}/${preflight_port}") 2>/dev/null; then
    exec 3>&- || true
    visto_unix_die "The configured address ${ADDRESS} is already in use. Stop the process using it, or install with a different --address."
  fi
fi
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
cp -R "${SOURCE_DIR}/scripts/." "${RELEASE_DIR}/scripts/"
[ -x "${RELEASE_DIR}/scripts/update-visto-server.sh" ] \
  || visto_unix_die "host scripts failed to copy into ${RELEASE_DIR}/scripts"
install -d -m 0755 "${RELEASE_DIR}/web"
# Copy the built SPA verbatim; it is a directory of static assets.  BSD
# install(1) on macOS has no GNU -D (create parents) and rejects it with
# "target directory ... does not exist"; cp -R preserves the tree and modes.
cp -R "${WEB_DIR}/." "${RELEASE_DIR}/web/"
[ -f "${RELEASE_DIR}/web/index.html" ] \
  || visto_unix_die "Studio web assets failed to copy into ${RELEASE_DIR}/web"
WEB_INSTALL_PATH="${VISTO_CURRENT_LINK}/web"

for declaration in LICENSE.txt NOTICE.txt THIRD_PARTY_NOTICES.md THIRD_PARTY.spdx.json; do
  install -m 0644 "${SOURCE_DIR}/${declaration}" "${RELEASE_DIR}/${declaration}"
done

# Linux Core uses a dedicated non-login account. Runtime/config/program remain
# administrator-owned; the service may write business data and logs only.
if [ "$VISTO_UNIX_OS" = linux ]; then
  VISTO_SERVICE_USER=${VISTO_SERVICE_USER:-visto}
  VISTO_SERVICE_GROUP=${VISTO_SERVICE_GROUP:-visto}
  for account in "$VISTO_SERVICE_USER" "$VISTO_SERVICE_GROUP"; do
    printf '%s' "$account" | grep -Eq '^[a-z_][a-z0-9_-]{0,31}$' \
      || visto_unix_die "invalid service account name"
  done
  command -v getent >/dev/null || visto_unix_die "getent is required to verify the service account"
  if ! getent passwd "$VISTO_SERVICE_USER" >/dev/null; then
    command -v useradd >/dev/null || visto_unix_die "useradd is required to create the service account"
    if ! getent group "$VISTO_SERVICE_GROUP" >/dev/null; then
      groupadd --system "$VISTO_SERVICE_GROUP"
    fi
    useradd --system --gid "$VISTO_SERVICE_GROUP" --no-create-home \
      --home-dir "$DATA_DIR" --shell /usr/sbin/nologin "$VISTO_SERVICE_USER"
  fi
  service_uid=$(id -u "$VISTO_SERVICE_USER")
  [ "$service_uid" != 0 ] || visto_unix_die "the Core service account must not be root"
  service_shell=$(getent passwd "$VISTO_SERVICE_USER" | cut -d: -f7)
  case "$service_shell" in */nologin|*/false) ;; *) visto_unix_die "the Core service account must be a non-login account" ;; esac
  getent group "$VISTO_SERVICE_GROUP" >/dev/null || visto_unix_die "service group does not exist"
  # Refuse aliases before privileged ownership changes. Do not follow links or
  # change hard-linked objects that could also name files outside this data root.
  [ ! -L "$DATA_DIR" ] || visto_unix_die "data directory must not be a symbolic link"
  [ ! -L "$VISTO_LOG_DIR" ] || visto_unix_die "log directory must not be a symbolic link"
  visto_unix_assign_service_data "$DATA_DIR" "$VISTO_SERVICE_USER" "$VISTO_SERVICE_GROUP"
  # systemd opens the log descriptors as root. The service must not replace
  # their names with links that a privileged restart could follow.
  chown root:"$VISTO_SERVICE_GROUP" "$VISTO_LOG_DIR"
  chmod 0750 "$DATA_DIR" "$VISTO_LOG_DIR"
fi

# --- Environment ------------------------------------------------------------
# The host management token is a local capability token for the bundled CLI.
# It is generated once per install and reused across restarts and updates.
TOKEN_FILE="${VISTO_CONFIG_DIR}/host-management-token"
if [ -s "$TOKEN_FILE" ]; then
  HOST_TOKEN="$(cat "$TOKEN_FILE")"
else
  HOST_TOKEN="$(visto_unix_generate_token)"
  printf '%s' "$HOST_TOKEN" > "$TOKEN_FILE"
fi
chmod 0600 "$TOKEN_FILE"
chown root:"$VISTO_ADMIN_GROUP" "$TOKEN_FILE"

cat > "$VISTO_ENV_FILE" <<EOF
REVIEW_STUDIO_ADDR=${ADDRESS}
REVIEW_STUDIO_DATA_DIR=${DATA_DIR}
VISTO_HOST_MANAGEMENT_TOKEN=${HOST_TOKEN}
VISTO_MEDIA_RUNTIME_ROOT=${VISTO_MEDIA_RUNTIME_ROOT}
VISTO_SERVER_UPDATE_SOURCES=${VISTO_SERVER_UPDATE_SOURCES}
EOF
if [ "$VISTO_UNIX_OS" = linux ]; then
  printf 'VISTO_SERVICE_USER=%s\nVISTO_SERVICE_GROUP=%s\n' \
    "$VISTO_SERVICE_USER" "$VISTO_SERVICE_GROUP" >> "$VISTO_ENV_FILE"
fi
# D2, docs/FREE_TIER_BOUNDARY_DESIGN.md 鎼?.4: the first-run wizard asks whether an
# Owner web session may add host directories as storage locations. The answer is
# recorded once and is not writable from the web. This override is the second and
# last way to change it: it needs host access, which is the point. Uncomment and
# set 0 to forbid web host paths on this host, or 1 to force them on.
cat >> "$VISTO_ENV_FILE" <<'EOF'
# VISTO_ALLOW_WEB_HOST_PATHS=0
EOF
printf 'REVIEW_STUDIO_WEB_DIR=%s\n' "$WEB_INSTALL_PATH" >> "$VISTO_ENV_FILE"
chmod 0600 "$VISTO_ENV_FILE"
chown root:"$VISTO_ADMIN_GROUP" "$VISTO_ENV_FILE"
visto_unix_info "wrote ${VISTO_ENV_FILE}"

# --- Service registration ---------------------------------------------------
# Both units start the service through the `current` symlink rather than the
# versioned release path, so switching releases does not require rewriting the
# unit. Both restart the service if it exits and start it at boot, which is what
# makes the server come back after a crash and after a reboot.

case "$VISTO_UNIX_OS" in
  macos)
    # launchctl print exposes job environment values to local users. Keep the
    # host-management token out of the plist and load it only in the root-owned
    # process immediately before exec. The environment file remains root-only.
    CORE_LAUNCHER="${VISTO_CONFIG_DIR}/run-visto-core.sh"
    cat > "$CORE_LAUNCHER" <<EOF
#!/bin/sh
set -eu
VISTO_HOST_MANAGEMENT_TOKEN=\$(cat "$TOKEN_FILE")
export VISTO_HOST_MANAGEMENT_TOKEN
exec "${VISTO_CURRENT_LINK}/bin/visto-core"
EOF
    chmod 0700 "$CORE_LAUNCHER"
    chown root:"$VISTO_ADMIN_GROUP" "$CORE_LAUNCHER"
    # RunAtLoad starts the job when launchd loads it (boot, or bootstrap here);
    # KeepAlive restarts it if it exits.
    cat > "$VISTO_LAUNCHD_PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${VISTO_SERVICE_NAME}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${CORE_LAUNCHER}</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>REVIEW_STUDIO_ADDR</key>
    <string>${ADDRESS}</string>
    <key>REVIEW_STUDIO_DATA_DIR</key>
    <string>${DATA_DIR}</string>
    <key>VISTO_MEDIA_RUNTIME_ROOT</key>
    <string>${VISTO_MEDIA_RUNTIME_ROOT}</string>
    <key>VISTO_SERVER_UPDATE_SOURCES</key>
    <string>${VISTO_SERVER_UPDATE_SOURCES}</string>
    <key>REVIEW_STUDIO_WEB_DIR</key>
    <string>${WEB_INSTALL_PATH}</string>
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
    chown root:"$VISTO_ADMIN_GROUP" "$VISTO_LAUNCHD_PLIST"
    visto_unix_info "wrote ${VISTO_LAUNCHD_PLIST}"
    ;;
  linux)
    # The token and the listen address live in the environment file rather than in
    # the unit, so they are not world-readable: the unit is 0644 and systemd reads
    # EnvironmentFile as root. macOS loads its token through the root-only
    # launcher because launchd has no equivalent file directive.
    #
    # Hardening notes: no NewPrivileges escalation path is needed because the unit
    # uses its dedicated account; ProtectHome is deliberately NOT set, because the whole
    # product is about reaching media that lives under /home. The kernel-level
    # protections below are the ones that do not constrain legitimate operation.
    cat > "$VISTO_SYSTEMD_UNIT" <<EOF
[Unit]
Description=Visto Server
Documentation=https://github.com/huangdiyou/Visto
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${VISTO_SERVICE_USER}
Group=${VISTO_SERVICE_GROUP}
EnvironmentFile=${VISTO_ENV_FILE}
ExecStart=${VISTO_CURRENT_LINK}/bin/visto-core
Restart=on-failure
RestartSec=3
# Killing the main process must not leave the media runtime or its children
# behind. SIGTERM first, then SIGKILL after a bounded grace period.
KillMode=control-group
KillSignal=SIGTERM
TimeoutStopSec=30
# /opt holds the program, /var the state, /etc the configuration; nothing else is
# writable by the service.
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectControlGroups=true
ProtectKernelTunables=true
ProtectKernelModules=true
ReadWritePaths=${DATA_DIR} ${VISTO_LOG_DIR}
ReadOnlyPaths=${VISTO_MEDIA_RUNTIME_ROOT} ${VISTO_CONFIG_DIR} ${VISTO_RELEASES_DIR}
StandardOutput=${VISTO_SYSTEMD_STDOUT}
StandardError=${VISTO_SYSTEMD_STDERR}

[Install]
WantedBy=multi-user.target
EOF
    chmod 0644 "$VISTO_SYSTEMD_UNIT"
    chown root:"$VISTO_ADMIN_GROUP" "$VISTO_SYSTEMD_UNIT"
    visto_unix_info "wrote ${VISTO_SYSTEMD_UNIT}"
    # systemd caches unit files; without this the freshly written unit is not
    # visible to enable/start.
    systemctl daemon-reload >/dev/null 2>&1 \
      || visto_unix_die "systemctl daemon-reload failed; the new unit is not registered"
    systemctl enable "$VISTO_SERVICE_NAME" >/dev/null 2>&1 \
      || visto_unix_die "systemctl enable ${VISTO_SERVICE_NAME} failed; the service would not start at boot"
    visto_unix_info "enabled ${VISTO_SERVICE_NAME} at boot"
    ;;
esac
visto_unix_info "wrote ${VISTO_LAUNCHD_PLIST}"

# --- Activate ---------------------------------------------------------------
visto_unix_switch_release "$RELEASE_DIR"
visto_unix_info "activated ${VISTO_CURRENT_LINK} -> ${RELEASE_DIR}"

if [ "$SKIP_START" = "1" ]; then
  visto_unix_info "skip-start requested; service is registered but not started"
  exit 0
fi


visto_unix_service_start
if ! visto_unix_wait_ready "$ADDRESS"; then
  visto_unix_error "Service was registered but did not become reachable. Check ${VISTO_LOG_DIR} and, on macOS, any Gatekeeper prompt for visto-core."
  exit 1
fi
visto_unix_info "service started"

# D2, docs/FREE_TIER_BOUNDARY_DESIGN.md 鎼?.5: print the effective host access
# state at the end of installation. The wizard has not run yet, so the answer is
# still open; saying so is more useful than printing a value that is about to
# change.
if [ "${VISTO_ALLOW_WEB_HOST_PATHS:-}" = "1" ]; then
  visto_unix_info "web host paths: forced ON by VISTO_ALLOW_WEB_HOST_PATHS (wizard choice will not apply)"
elif [ "${VISTO_ALLOW_WEB_HOST_PATHS:-}" = "0" ]; then
  visto_unix_info "web host paths: forced OFF by VISTO_ALLOW_WEB_HOST_PATHS (wizard choice will not apply)"
else
  visto_unix_info "web host paths: not decided yet; the first-run wizard asks once (default: allowed)"
fi
