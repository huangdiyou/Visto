#!/usr/bin/env bash
# Install the supported video component using the existing signed manager.
# Administrator confirmation is required; no unverified payload is executed.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
. "${SCRIPT_DIR}/visto-server-env.sh"
CONFIRM=0
SERVER_BINARY=""
while [ $# -gt 0 ]; do
  case "$1" in
    --confirm-download) CONFIRM=1; shift ;;
    --server-binary) SERVER_BINARY="${2:?--server-binary requires a path}"; shift 2 ;;
    -h|--help) printf '%s\n' 'sudo bash scripts/install-media-runtime.sh --confirm-download'; exit 0 ;;
    *) visto_unix_die "unknown option: $1" ;;
  esac
done
[ "$CONFIRM" = 1 ] || visto_unix_die "Add --confirm-download to authorize downloading the video component."
visto_unix_detect_platform
visto_unix_resolve_paths
visto_unix_require_root
[ "$VISTO_UNIX_OS" != linux ] || umask 022
: "${SERVER_BINARY:=${VISTO_CURRENT_LINK}/bin/visto-server}"
[ -x "$SERVER_BINARY" ] || visto_unix_die "Visto Server is missing. Install the server first."
case "${VISTO_UNIX_OS}:$(uname -m)" in
  macos:arm64) RUNTIME_VERSION=ffmpeg-8.1.2-macos-lgpl.2 ;;
  linux:x86_64) RUNTIME_VERSION=ffmpeg-8.1.2-linux-amd64-lgpl.2 ;;
  linux:aarch64|linux:arm64) RUNTIME_VERSION=ffmpeg-8.1.2-linux-arm64-lgpl.2 ;;
  *) visto_unix_die "This platform has no supported video component." ;;
esac
if [ -z "${VISTO_MEDIA_RUNTIME_ROOT:-}" ]; then
  case "${VISTO_UNIX_OS}:${VISTO_PREFIX}" in
    macos:/Library/Visto) VISTO_MEDIA_RUNTIME_ROOT='/Library/Application Support/Visto/runtime' ;;
    linux:/opt/visto) VISTO_MEDIA_RUNTIME_ROOT=/var/lib/visto-runtime ;;
    *) VISTO_MEDIA_RUNTIME_ROOT="${VISTO_PREFIX}/media-runtime" ;;
  esac
fi
export VISTO_MEDIA_RUNTIME_ROOT
command -v curl >/dev/null || visto_unix_die "curl is required to download the video component."
# This is the formal public trust root, not a key supplied by a downloaded archive.
PUBLIC_KEY='lMcaxJOtCeDaiJgeW0S5Gh/m3PYQ4WG7ijIs9yxVZ2M='
BASE="https://visto-server-updates.pages.dev/media-runtime/${RUNTIME_VERSION}"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/visto-media-install.XXXXXX")
trap 'rm -rf "$WORK"' EXIT
for name in media-runtime-manifest.json media-runtime-manifest.json.sig; do
  curl --fail --show-error --silent --location --proto '=https' --proto-redir '=https' \
    --connect-timeout 20 --max-time 120 "$BASE/$name" --output "$WORK/$name"
done
visto_unix_info "Downloading and verifying the video component..."
# The host CLI writes one line per downloaded chunk (throttled, on stderr), which
# buries the output an operator actually has to read. When a terminal is attached
# those lines are rendered as one in-place progress bar; without one the output is
# kept verbatim so logs and CI keep every original line. Nothing is dropped in
# either case: this only changes presentation, never the exit code or the payload.
format_download_progress() {
  local line name rest tail_ got total pct width=28 filled=0 index=0 bar='' last=-1
  while IFS= read -r line; do
    case "$line" in
      *[Dd]ownload:*bytes*)
        rest=${line#*[Dd]ownload: }
        got=${rest%% *}
        tail_=${rest#* / }
        total=${tail_%% *}
        case "$got$total" in
          ''|*[!0-9]*) printf '\r\033[K%s\n' "$line"; continue ;;
        esac
        if [ "$total" -le 0 ]; then printf '\r\033[K%s\n' "$line"; continue; fi
        name=${line%% [Dd]ownload:*}
        pct=$(( got * 100 / total ))
        [ "$pct" = "$last" ] && [ "$got" != "$total" ] && continue
        last=$pct
        filled=$(( pct * width / 100 ))
        bar=''
        index=0
        while [ "$index" -lt "$width" ]; do
          if [ "$index" -lt "$filled" ]; then bar="#$bar"; else bar=".$bar"; fi
          index=$(( index + 1 ))
        done
        printf '\r\033[K视频组件 %s [%s] %3d%%  %d / %d MB' "${name:-download}" "$bar" "$pct" \
          "$(( got / 1048576 ))" "$(( total / 1048576 ))"
        ;;
      *)
        printf '\r\033[K%s\n' "$line"
        ;;
    esac
  done
  printf '\n'
}
set +e
if [ -t 1 ]; then
  "$SERVER_BINARY" media-runtime install --media-runtime managed \
    --manifest "$WORK/media-runtime-manifest.json" \
    --signature "$WORK/media-runtime-manifest.json.sig" \
    --public-key "$PUBLIC_KEY" --non-interactive --confirm-download 2>&1 \
    | format_download_progress
  install_status=${PIPESTATUS[0]}
else
  "$SERVER_BINARY" media-runtime install --media-runtime managed \
    --manifest "$WORK/media-runtime-manifest.json" \
    --signature "$WORK/media-runtime-manifest.json.sig" \
    --public-key "$PUBLIC_KEY" --non-interactive --confirm-download 2>&1
  install_status=$?
fi
set -e
[ "$install_status" = 0 ] \
  || visto_unix_die "Video component installation failed (exit ${install_status}). No further step was taken."
if [ "$VISTO_UNIX_OS" = linux ]; then
  visto_unix_public_payload "$VISTO_MEDIA_RUNTIME_ROOT/releases"
fi
visto_unix_info "Video component installed. Restart an already running server to use it."
