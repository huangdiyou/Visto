#!/usr/bin/env bash
# Standalone website installer. Build embeds the legacy-package helpers below.
set -Eeuo pipefail
umask 077
REPOSITORY='https://github.com/huangdiyou/Visto'
INSTALLER_PATH=$(cd "$(dirname "$0")" && pwd -P)/$(basename "$0")
ORIGINAL_ARGS=("$@")
SOURCE_DIR=''
PACKAGE=''
RESUME_DIR=''
VERSION=''
CHECK_ONLY=0
NON_INTERACTIVE=0
CONFIRMED=0
SETUP_ARGS=()
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
while [ $# -gt 0 ]; do
  case "$1" in
    --source-dir) SOURCE_DIR="${2:?--source-dir needs a folder}"; shift 2 ;;
    --package) PACKAGE="${2:?--package needs an archive}"; shift 2 ;;
    --resume-dir) RESUME_DIR="${2:?--resume-dir needs the retained installation folder}"; shift 2 ;;
    --check-only) CHECK_ONLY=1; shift ;;
    --address) SETUP_ARGS+=(--address "${2:?--address needs an address}"); shift 2 ;;
    --confirm-download) CONFIRMED=1; SETUP_ARGS+=("$1"); shift ;;
    --non-interactive) NON_INTERACTIVE=1; SETUP_ARGS+=("$1"); shift ;;
    -h|--help)
      printf '%s\n' 'Visto installation' \
        'macOS: bash install-visto.sh (administrator password requested after approval)' \
        'Linux: sudo bash install-visto.sh' \
        'Downloaded folder: bash install-visto.sh --source-dir .' \
        'Downloaded archive: sudo bash install-visto.sh --package PATH' \
        'Retry retained macOS files: bash install-visto.sh --resume-dir PATH' \
        'Check without installing: --check-only' \
        'Options: --address 127.0.0.1:PORT --non-interactive --confirm-download'
      exit 0 ;;
    *) die "Unknown option: $1" ;;
  esac
done
[ -z "$SOURCE_DIR" ] || [ -z "$PACKAGE" ] || die 'Choose a folder or an archive, not both.'
[ -z "$RESUME_DIR" ] || { [ -z "$SOURCE_DIR" ] && [ -z "$PACKAGE" ]; } \
  || die 'Resume cannot be combined with another package source.'
[ "$NON_INTERACTIVE" = 0 ] || [ "$CONFIRMED" = 1 ] || [ "$CHECK_ONLY" = 1 ] \
  || die 'Unattended installation needs --confirm-download.'
case "$(uname -s):$(uname -m)" in
  Darwin:arm64) PLATFORM=macos-arm64 ;;
  Linux:x86_64) PLATFORM=linux-amd64 ;;
  Linux:aarch64|Linux:arm64) PLATFORM=linux-arm64 ;;
  *) die 'Supported: Apple Silicon macOS; systemd Linux x86_64 / ARM64.' ;;
esac
# Linux services run as an unprivileged account. Public package assets and
# verified runtime binaries must remain readable after extraction/creation.
# Private tokens/configuration retain the explicit 0600 modes in the installer.
[ "$PLATFORM" = macos-arm64 ] || umask 022
if [ "$PLATFORM" = macos-arm64 ] && [ "$CHECK_ONLY" = 0 ] && [ "$(id -u)" = 0 ]; then
  [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ] \
    || die 'macOS: run bash install-visto.sh without sudo, from your normal user terminal.'
  [ "$(id -u "$SUDO_USER")" = "${SUDO_UID:-}" ] && [ "$SUDO_UID" != 0 ] \
    || die 'Cannot determine the original macOS user. Run without sudo.'
  printf '先以当前用户完成 macOS 放行，之后再申请安装权限。\n'
  if [ "${#ORIGINAL_ARGS[@]}" -gt 0 ]; then
    exec sudo -H -u "$SUDO_USER" /bin/bash "$INSTALLER_PATH" "${ORIGINAL_ARGS[@]}"
  else
    exec sudo -H -u "$SUDO_USER" /bin/bash "$INSTALLER_PATH"
  fi
fi
command -v curl >/dev/null || die 'curl is required.'
command -v tar >/dev/null || die 'tar is required.'
valid_version() { [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][a-zA-Z0-9.-]+)?$ ]]; }
stable_version() { [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+][a-zA-Z0-9.-]+)?$ ]]; }
WORK=$(mktemp -d "${TMPDIR:-/tmp}/visto-install.XXXXXX")
WORK=$(cd "$WORK" && pwd -P)
KEEP_WORK=0
trap '[ "$KEEP_WORK" = 1 ] || rm -rf "$WORK"' EXIT
trap 'printf "安装未完成。请保留上方错误，已有数据不会因此被删除。\n" >&2' ERR
fetch() {
  curl --fail --show-error --location --proto '=https' --proto-redir '=https' \
    --connect-timeout 20 --max-time 600 --retry 2 "$1" --output "$2"
}
version_from_name() {
  local name="$1" suffix="$2"
  case "$name" in Visto-Server_*_"$PLATFORM""$suffix") ;; *) return 1 ;; esac
  name=${name#Visto-Server_}; name=${name%_"$PLATFORM""$suffix"}
  valid_version "$name" || return 1
  printf '%s' "$name"
}
# The exact same generated entry travels in every Unix package. A neighbouring
# version file selects local bytes; the website copy still resolves Latest.
if [ -z "$SOURCE_DIR$PACKAGE$RESUME_DIR" ] && [ -f "$(dirname "$INSTALLER_PATH")/package-version" ]; then
  SOURCE_DIR=$(cd "$(dirname "$INSTALLER_PATH")/.." && pwd -P)
fi
if [ -n "$RESUME_DIR" ]; then
  [ "$PLATFORM" = macos-arm64 ] || die 'Retained approval files are only used on macOS.'
  RESUME_DIR=$(cd "$RESUME_DIR" && pwd -P)
  [ -f "$RESUME_DIR/scripts/package-version" ] || die 'Retained package version is missing.'
  VERSION=$(cat "$RESUME_DIR/scripts/package-version")
  valid_version "$VERSION" || die 'Invalid retained package version.'
  printf '继续安装已保留的 Visto %s，不重新下载或复制程序。\n' "$VERSION"
elif [ -n "$SOURCE_DIR" ]; then
  SOURCE_DIR=$(cd "$SOURCE_DIR" && pwd -P)
  if [ -f "$SOURCE_DIR/scripts/package-version" ]; then
    VERSION=$(cat "$SOURCE_DIR/scripts/package-version")
  else
    VERSION=$(version_from_name "${SOURCE_DIR##*/}" '') \
      || die 'Keep the original extracted folder name, such as Visto-Server_1.0.3_macos-arm64.'
  fi
  valid_version "$VERSION" || die 'Invalid package version.'
  mkdir "$WORK/package"
  for directory in bin web scripts; do
    [ -d "$SOURCE_DIR/$directory" ] || die "Missing package folder: $directory"
    cp -R "$SOURCE_DIR/$directory" "$WORK/package/$directory"
  done
  for notice in LICENSE.txt NOTICE.txt THIRD_PARTY_NOTICES.md THIRD_PARTY.spdx.json THIRD_PARTY_DISTRIBUTION_INVENTORY.md; do
    [ ! -f "$SOURCE_DIR/$notice" ] || cp "$SOURCE_DIR/$notice" "$WORK/package/$notice"
  done
  printf '正在安装 Visto %s。\n' "$VERSION"
else
  if [ -n "$PACKAGE" ]; then
    [ -f "$PACKAGE" ] || die 'The downloaded archive was not found.'
    PACKAGE=$(cd "$(dirname "$PACKAGE")" && pwd -P)/$(basename "$PACKAGE")
    VERSION=$(version_from_name "${PACKAGE##*/}" '.tar.gz') \
      || die 'Keep the original archive filename for this platform.'
    TAG="v$VERSION"
    ARCHIVE=${PACKAGE##*/}
  else
    # Resolve Latest once. All payloads below use the resulting immutable tag.
    TAG_URL=$(curl --fail --show-error --silent --head --proto '=https' \
      --connect-timeout 20 --max-time 60 --header 'Cache-Control: no-cache' \
      --output /dev/null --write-out '%{redirect_url}' "$REPOSITORY/releases/latest") \
      || die 'Cannot read the latest release. Check network access and whether the repository is public.'
    case "$TAG_URL" in "$REPOSITORY/releases/tag/"*) TAG=${TAG_URL#"$REPOSITORY/releases/tag/"} ;;
      *) die 'GitHub did not return a latest release tag.' ;; esac
    VERSION=${TAG#v}
    stable_version "$VERSION" || die 'Latest is not a supported stable version.'
    ARCHIVE="Visto-Server_${VERSION}_${PLATFORM}.tar.gz"
    PACKAGE="$WORK/$ARCHIVE"
    printf '最新正式版：Visto %s · %s\n正在下载安装包…\n' "$VERSION" "$PLATFORM"
    fetch "$REPOSITORY/releases/download/$TAG/$ARCHIVE" "$PACKAGE"
  fi
  # Users never have to download or compare this sidecar themselves.
  if [ -f "$PACKAGE.sha256" ]; then
    cp "$PACKAGE.sha256" "$WORK/checksum"
  else
    fetch "$REPOSITORY/releases/download/$TAG/$ARCHIVE.sha256" "$WORK/checksum"
  fi
  EXPECTED=$(awk -v name="$ARCHIVE" '
    {sub(/\r$/, ""); lines++
     if (NR == 1 && NF == 2 && $1 == "SHA256" && $2 == name) {legacy=1; next}
     if (legacy) {if (NR != 2 || NF != 1) bad=1; digest=$1}
     else {if (NF != 2 || ($2 != name && $2 != "*" name)) bad=1; digest=$1}
     if (length(digest) != 64 || digest ~ /[^0-9a-fA-F]/) bad=1}
    END {if (bad || lines != (legacy ? 2 : 1)) exit 1; print tolower(digest)}' "$WORK/checksum") \
    || die 'The release checksum file does not match this package.'
  if command -v sha256sum >/dev/null; then
    ACTUAL=$(sha256sum "$PACKAGE" | awk '{print $1}')
  else
    ACTUAL=$(shasum -a 256 "$PACKAGE" | awk '{print $1}')
  fi
  [ "$ACTUAL" = "$EXPECTED" ] || die 'Package verification failed; nothing was installed.'
  tar -tzf "$PACKAGE" > "$WORK/members"
  tar -tvzf "$PACKAGE" > "$WORK/types"
  # Official Server packages contain only plain files/directories, no links.
  awk 'substr($0,1,1) != "-" && substr($0,1,1) != "d" {bad=1} END {exit bad}' "$WORK/types" \
    || die 'The archive contains unsupported links or special files.'
  while IFS= read -r member; do
    case "$member" in /*|*[!a-zA-Z0-9_./+-]*) die 'Unsafe package entry.' ;; esac
    [[ ! "$member" =~ (^|/)\.\.(/|$) ]] || die 'Unsafe package path.'
  done < "$WORK/members"
  mkdir "$WORK/package"
  tar -xzf "$PACKAGE" --no-same-owner --no-same-permissions -C "$WORK/package"
fi
STAGED=${RESUME_DIR:-"$WORK/package"}
for file in bin/visto-server bin/visto-core web/index.html scripts/visto-server-env.sh scripts/install-visto-server.sh; do
  [ -f "$STAGED/$file" ] && [ ! -L "$STAGED/$file" ] || die "Incomplete package: $file"
done
if [ -f "$STAGED/scripts/package-version" ]; then
  [ "$(cat "$STAGED/scripts/package-version")" = "$VERSION" ] || die 'Package version metadata disagrees with the selected release.'
else
  printf '%s\n' "$VERSION" > "$STAGED/scripts/package-version"
fi
if [ "$PLATFORM" != macos-arm64 ] || [ ! -f "$STAGED/scripts/setup-visto-server.sh" ]; then
  # @VISTO_LEGACY_HELPERS@
  :
fi
if [ "$CHECK_ONLY" = 1 ]; then
  printf '已检查 Visto %s · %s 的安装材料；没有安装、启动或修改服务。\n' "$VERSION" "$PLATFORM"
  exit 0
fi
if [ "$PLATFORM" = macos-arm64 ]; then
  # Approval must apply to the same executable bytes and installation helpers.
  approval_snapshot() {
    local file digest
    : > "$WORK/approval-digests"
    for file in bin/visto-server bin/visto-core scripts/package-version scripts/install-visto-server.sh \
      scripts/visto-server-env.sh scripts/setup-visto-server.sh scripts/install-media-runtime.sh; do
      [ -f "$STAGED/$file" ] && [ ! -L "$STAGED/$file" ] || die "Unsafe installation file: $file"
      if command -v sha256sum >/dev/null; then digest=$(sha256sum "$STAGED/$file" | awk '{print $1}')
      else digest=$(shasum -a 256 "$STAGED/$file" | awk '{print $1}'); fi
      printf '%s  %s\n' "$digest" "$file" >> "$WORK/approval-digests"
    done
  }
  check_approval_files() {
    approval_snapshot
    [ -f "$STAGED/.approval-digests" ] && [ ! -L "$STAGED/.approval-digests" ] \
      && cmp -s "$WORK/approval-digests" "$STAGED/.approval-digests" \
      || die 'Retained installation files changed. Start again with a fresh verified package.'
  }
  retain_approval_files() {
    [ -n "$RESUME_DIR" ] || KEEP_WORK=1
    printf '安装文件已保留：%s\n下次继续时可使用以下命令，不会重新下载或替换这份程序：\n' "$STAGED" >&2
    printf 'bash %q --resume-dir %q' "$INSTALLER_PATH" "$STAGED" >&2
    if [ "${#SETUP_ARGS[@]}" -gt 0 ]; then printf ' %q' "${SETUP_ARGS[@]}" >&2; fi
    printf '\n' >&2
  }
  approval_snapshot
  if [ -n "$RESUME_DIR" ]; then
    check_approval_files
  else
    cp "$WORK/approval-digests" "$STAGED/.approval-digests"
  fi
  # Check the exact files used for installation, under the desktop user's identity.
  # The 1.0.3 Core ignores --help: explicitly clear its required host token so it
  # stops before database/startup. Server's help deliberately returns exit 2.
  for name in visto-server visto-core; do
    binary="$STAGED/bin/$name"
    settings_opened=0
    while :; do
      check_approval_files
      help_status=0
      env VISTO_HOST_MANAGEMENT_TOKEN= "$binary" --help > "$WORK/$name-help.log" 2>&1 || help_status=$?
      help_ok=0
      if [ "$help_status" = 0 ]; then help_ok=1
      elif [ "$help_status" = 2 ] && grep -Eq '^Usage(:| of )' "$WORK/$name-help.log"; then help_ok=1
      elif [ "$name" = visto-core ] && [ "$help_status" = 1 ] \
        && grep -Fq 'VISTO_HOST_MANAGEMENT_TOKEN is required to start Core' "$WORK/$name-help.log"; then help_ok=1
      fi
      [ "$help_ok" = 0 ] || break
      [ -n "$RESUME_DIR" ] || KEEP_WORK=1
      cat "$WORK/$name-help.log" >&2
      printf '\n%s 未能运行（退出码 %s），安装尚未开始。\n' "$name" "$help_status" >&2
      printf '如出现 macOS 拦截，请点击“完成”，打开“系统设置 → 隐私与安全性”，在下方“安全性”点击“仍要打开”。\n' >&2
      # SIGKILL is the observed macOS block. Other failures are not assumed to
      # be Gatekeeper; unattended runs never wait or open desktop settings.
      if [ "$help_status" != 137 ] || [ "$NON_INTERACTIVE" = 1 ] || [ ! -t 0 ]; then
        retain_approval_files
        exit 72
      fi
      trap 'printf "\n安装已暂停。\n" >&2; retain_approval_files; exit 130' INT
      trap 'printf "\n安装已暂停。\n" >&2; retain_approval_files; exit 143' TERM
      if [ "$settings_opened" = 0 ]; then
        settings_opened=1
        # Best effort: the user still approves the program in Apple's UI.
        if command -v open >/dev/null; then
          open 'x-apple.systempreferences:com.apple.preference.security?General' >/dev/null 2>&1 || true
        fi
      fi
      printf '请保留此终端窗口。系统再次弹出确认时，完成“打开”或“允许”。\n' >&2
      while :; do
        printf '放行完成后回到这里按回车继续（输入 q 暂停安装）：' >&2
        if ! IFS= read -r APPROVAL_ANSWER; then retain_approval_files; exit 72; fi
        case "$APPROVAL_ANSWER" in
          '') break ;;
          q|Q) printf '安装已暂停。\n' >&2; retain_approval_files; exit 72 ;;
          *) printf '完成系统确认后按回车；暂时不安装请输入 q。\n' >&2 ;;
        esac
      done
    done
  done
  check_approval_files
  trap - INT TERM
  KEEP_WORK=0
  printf '两个程序均可运行，接下来需要管理员权限安装。\n'
  RUN_SETUP=(sudo /bin/bash)
else
  [ "$(id -u)" = 0 ] || die 'Run with sudo: sudo bash install-visto.sh [options].'
  RUN_SETUP=(bash)
fi
if [ "${#SETUP_ARGS[@]}" -gt 0 ]; then
  "${RUN_SETUP[@]}" "$STAGED/scripts/setup-visto-server.sh" "${SETUP_ARGS[@]}"
else
  "${RUN_SETUP[@]}" "$STAGED/scripts/setup-visto-server.sh"
fi
