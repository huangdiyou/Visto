#!/usr/bin/env bash
# Local, guided installation. One entry installs video support and Server.
# The browser setup page never downloads software or requests host privileges.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
. "${SCRIPT_DIR}/visto-server-env.sh"
SOURCE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd -P)"
ADDRESS="127.0.0.1:8787"
EXPLICIT_ADDRESS=0
CONFIRMED=0
NON_INTERACTIVE=0
SKIP_START=0
while [ $# -gt 0 ]; do
  case "$1" in
    --address) ADDRESS="${2:?--address needs a value}"; EXPLICIT_ADDRESS=1; shift 2 ;;
    --confirm-download) CONFIRMED=1; shift ;;
    --non-interactive) NON_INTERACTIVE=1; shift ;;
    --skip-start) SKIP_START=1; shift ;;
    -h|--help)
      printf '%s\n' 'sudo bash scripts/setup-visto-server.sh' \
        'Installs Server and verified video support. Prompts before downloading.' \
        'Unattended: --non-interactive --confirm-download [--address 127.0.0.1:PORT]'
      exit 0 ;;
    *) visto_unix_die "unknown option: $1" ;;
  esac
done
visto_unix_detect_platform
visto_unix_resolve_paths
visto_unix_require_root
trap 'printf "\n安装未完成。请保留上方错误；不要删除已有数据。\n" >&2' ERR

if [ -f "$SCRIPT_DIR/package-version" ]; then
  VERSION=$(cat "$SCRIPT_DIR/package-version")
else
  # Allows the separate setup helper to work with an untouched 1.0.3 package.
  # Renamed legacy directories are refused rather than guessing a version.
  VERSION=${SOURCE_DIR##*/}
  VERSION=${VERSION#Visto-Server_}
  VERSION=${VERSION%_${VISTO_UNIX_PLATFORM}}
fi
case "$VERSION" in
  *$'\n'*|*$'\r'*) visto_unix_die "Invalid multi-line package version metadata" ;;
esac
visto_unix_assert_version "$VERSION"
[ -x "$SOURCE_DIR/bin/visto-server" ] || visto_unix_die "missing Visto Server"
[ -f "$SCRIPT_DIR/install-media-runtime.sh" ] || visto_unix_die "missing video component installer"
REPAIR=0
if [ -e "$VISTO_CURRENT_LINK" ] || [ -L "$VISTO_CURRENT_LINK" ]; then
  [ -L "$VISTO_CURRENT_LINK" ] && [ "$(readlink "$VISTO_CURRENT_LINK")" = "$VISTO_RELEASES_DIR/$VERSION" ] \
    || visto_unix_die "An existing different installation was found. Use its update instructions."
  REPAIR=1
  visto_unix_read_env
  ADDRESS=$VISTO_SERVER_ADDRESS
  EXISTING_RUNTIME_ROOT=$(visto_unix_env_value VISTO_MEDIA_RUNTIME_ROOT)
  [ -z "$EXISTING_RUNTIME_ROOT" ] || export VISTO_MEDIA_RUNTIME_ROOT="$EXISTING_RUNTIME_ROOT"
elif [ -e "$VISTO_ENV_FILE" ] || [ -L "$VISTO_ENV_FILE" ]; then
  visto_unix_die "Retained configuration found without an active release. Preserve it and inspect the previous installation before retrying."
elif [ -e "$VISTO_RELEASES_DIR/$VERSION" ]; then
  visto_unix_die "An incomplete installation already exists. Keep it and inspect the previous error."
fi
[[ "$ADDRESS" =~ ^127\.0\.0\.1:([0-9]{1,5})$ ]] || visto_unix_die "Use a local address: 127.0.0.1:PORT"
PORT=${ADDRESS##*:}
[ "$PORT" -ge 1024 ] && [ "$PORT" -le 65535 ] || visto_unix_die "Port must be between 1024 and 65535"
port_busy() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
if [ "$REPAIR" = 0 ] && port_busy "$PORT"; then
  [ "$EXPLICIT_ADDRESS" = 0 ] && [ "$NON_INTERACTIVE" = 0 ] \
    || visto_unix_die "${ADDRESS} is occupied. Choose a different --address; nothing was installed."
  for PORT in $(seq 8788 8800); do
    if ! port_busy "$PORT"; then break; fi
  done
  ! port_busy "$PORT" || visto_unix_die "No free installation port was found"
  printf '默认端口 8787 已被占用，原程序会保留。改用 127.0.0.1:%s？[Y/n] ' "$PORT"
  read -r ANSWER || visto_unix_die "No confirmation received"
  case "$ANSWER" in ''|y|Y|yes|YES) ADDRESS="127.0.0.1:$PORT" ;; *) printf '已取消，没有开始安装。\n'; exit 0 ;; esac
fi

printf '\nVisto 本地安装向导 · %s\n' "$VERSION"
printf '工作空间地址：http://%s/\n' "$ADDRESS"
printf '视频组件：FFmpeg / ffprobe，用于封面和视频预览。\n'
printf '来源：Visto 审核的 LGPL 组件；下载、验证和配置由安装器完成。\n'
if [ "$REPAIR" = 1 ]; then
  printf '已检测到同版本 Server。将补齐视频组件并重启服务，程序和项目数据不覆盖。\n'
else
  printf '将安装视频组件、注册 Server 服务，然后打开首次配置。\n'
fi
if [ "$CONFIRMED" = 0 ]; then
  [ "$NON_INTERACTIVE" = 0 ] || visto_unix_die "Unattended installation needs --confirm-download"
  printf '下载视频组件并继续？[Y/n] '
  read -r ANSWER || visto_unix_die "No confirmation received"
  case "$ANSWER" in ''|y|Y|yes|YES) ;; *) printf '已取消，没有开始安装。\n'; exit 0 ;; esac
fi
printf '\n[1/3] 准备视频预览组件…\n'
if [ "$VISTO_UNIX_OS" = linux ] && [ -z "${VISTO_MEDIA_RUNTIME_ROOT:-}" ]; then
  if [ "$VISTO_PREFIX" = /opt/visto ]; then
    export VISTO_MEDIA_RUNTIME_ROOT=/var/lib/visto-runtime
  else
    export VISTO_MEDIA_RUNTIME_ROOT="$VISTO_PREFIX/media-runtime"
  fi
fi
bash "$SCRIPT_DIR/install-media-runtime.sh" --server-binary "$SOURCE_DIR/bin/visto-server" --confirm-download
# Repair public, immutable payloads left by older website installers (umask
# 077). Never touch runtime state, configuration, tokens, logs or project data.
# Do not follow links or chmod hard-linked files outside these payload trees.
if [ "$VISTO_UNIX_OS" = linux ]; then
  visto_unix_public_payload "$SOURCE_DIR/web"
  if [ "$REPAIR" = 1 ]; then
    visto_unix_public_payload "$VISTO_RELEASES_DIR/$VERSION/web"
  fi
fi
printf '\n[2/3] 安装并启动 Server…\n'
if [ "$REPAIR" = 1 ]; then
  if [ "$SKIP_START" = 0 ]; then
    case "$VISTO_UNIX_OS" in
      macos) launchctl kickstart -k "system/$VISTO_SERVICE_NAME" ;;
      linux) systemctl restart "$VISTO_SERVICE_NAME" ;;
    esac
  fi
else
  ARGS=(--source-dir "$SOURCE_DIR" --version "$VERSION" --address "$ADDRESS")
  [ "$SKIP_START" = 0 ] || ARGS+=(--skip-start)
  bash "$SCRIPT_DIR/install-visto-server.sh" "${ARGS[@]}"
fi
if [ "$SKIP_START" = 1 ]; then
  printf '已按要求跳过启动，尚未验证服务与视频预览。\n'
  exit 0
fi
printf '\n[3/3] 检查视频组件与工作空间…\n'
HEALTH=''
for attempt in $(seq 1 45); do
  if HEALTH=$(curl --fail --silent --show-error --max-time 2 "http://$ADDRESS/health/ready" 2>/dev/null); then
    break
  fi
  HEALTH=''
  sleep 2
done
[ -n "$HEALTH" ] || visto_unix_die "Server did not become ready after startup; inspect the service logs"
printf '%s' "$HEALTH" | grep -Eq '"ffmpeg"[[:space:]]*:[[:space:]]*true' \
  || visto_unix_die "Server started, but video processing is not ready"
printf '%s' "$HEALTH" | grep -Eq '"ffprobe"[[:space:]]*:[[:space:]]*true' \
  || visto_unix_die "Server started, but media inspection is not ready"
if [ "$VISTO_UNIX_OS" = linux ]; then
  HOME_PAGE=$(curl --fail --silent --show-error --max-time 10 "http://$ADDRESS/")
  printf '%s' "$HOME_PAGE" | grep -Fq 'id="root"' \
    || visto_unix_die "Server started, but the workspace page is not available"
fi
printf '\n安装完成。打开 http://%s/ 创建工作空间。\n' "$ADDRESS"
