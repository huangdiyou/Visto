#!/usr/bin/env bash
#
# Build the native macOS Server package.
#
# Produces the package and the local verification evidence the release gate consumes:
#
#   Visto-Server_<version>_macos-arm64.tar.gz              the package
#   Visto-Server_<version>_macos-arm64.tar.gz.sha256       checksum sidecar
#   Visto-Server_<version>_macos-arm64.tar.gz.audit.json   contract audit
#   checksums-macos-arm64.txt                              per-platform checksums
#
# Signed update metadata is produced by the release signing stage rather than
# by this platform-specific packaging script.
#
# The package layout is enforced by the shared native package contract. Legal
# declaration files are copied from release/server; missing or placeholder
# material makes the build or package audit fail.
#
# The script runs from the repository root with relative paths. A build only
# proves the recipe produced a contract-compliant artifact; target-host runtime
# verification remains a separate release check.
#
# Usage:
#   ./scripts/build-server-macos.sh [<version>] [--arch arm64|amd64]
#       [--output <dir>] [--skip-web]
#
# Environment:
#   VISTO_CANDIDATE_AUDIT=0   skip the contract audit (not for releases)
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

VERSION="1.0.0-dev"
ARCH="arm64"
OUTPUT_DIR="dist/server"
SKIP_WEB=0

while [ $# -gt 0 ]; do
  case "$1" in
    --arch) ARCH="${2:?--arch requires arm64 or amd64}"; shift 2 ;;
    --output) OUTPUT_DIR="${2:?--output requires a directory}"; shift 2 ;;
    --skip-web) SKIP_WEB=1; shift ;;
    -h|--help) sed -n '2,32p' "$0"; exit 0 ;;
    -*) printf 'unknown option: %s\n' "$1" >&2; exit 2 ;;
    *) VERSION="$1"; shift ;;
  esac
done

case "$ARCH" in
  arm64|amd64) ;;
  *) printf 'unsupported arch: %s (expected arm64 or amd64)\n' "$ARCH" >&2; exit 2 ;;
esac

cd "${REPO_ROOT}"

for tool in go node npm tar; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    printf 'required build tool "%s" is not available on PATH\n' "$tool" >&2
    exit 1
  fi
done

FILE_TOKEN="macos-${ARCH}"
PACKAGE_NAME="Visto-Server_${VERSION}_${FILE_TOKEN}"
PACKAGE_ROOT="${OUTPUT_DIR}/${PACKAGE_NAME}"
ARCHIVE="${OUTPUT_DIR}/${PACKAGE_NAME}.tar.gz"

sha256_of() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

# --- assemble -----------------------------------------------------------------

rm -rf "${PACKAGE_ROOT}" "${ARCHIVE}" "${ARCHIVE}.sha256" "${ARCHIVE}.audit.json"
mkdir -p "${PACKAGE_ROOT}/bin" "${PACKAGE_ROOT}/web" "${PACKAGE_ROOT}/scripts"

printf 'Building Visto Server Core for darwin/%s...\n' "$ARCH"
GOOS=darwin GOARCH="${ARCH}" CGO_ENABLED=0 \
  go build -trimpath -ldflags "-X main.version=${VERSION}" \
  -o "${PACKAGE_ROOT}/bin/visto-core" ./services/core/cmd/server
GOOS=darwin GOARCH="${ARCH}" CGO_ENABLED=0 \
  go build -trimpath -ldflags "-X main.version=${VERSION}" \
  -o "${PACKAGE_ROOT}/bin/visto-server" ./services/core/cmd/visto-server

if [ "$SKIP_WEB" -eq 0 ]; then
  printf 'Building Studio web assets...\n'
  npm run build --workspace @review-studio/web
  cp -R "apps/web/dist/." "${PACKAGE_ROOT}/web/"
fi

if [ ! -f "${PACKAGE_ROOT}/web/index.html" ]; then
  printf 'web/index.html is required by the package contract and was not produced\n' >&2
  exit 1
fi

printf 'Staging the host scripts...\n'
for script in \
  visto-server-env.sh \
  install-visto-server.sh \
  uninstall-visto-server.sh \
  backup-visto-server.sh \
  restore-visto-server.sh \
  update-visto-server.sh \
  rollback-visto-server.sh; do
  cp "scripts/server-unix/${script}" "${PACKAGE_ROOT}/scripts/${script}"
done

printf 'Copying the required declaration files from repository material...\n'
# Contract section 2.2. Every one of these is a real repository file: there is
# no generated or placeholder licence anywhere in this path.
cp release/server/LICENSE.txt "${PACKAGE_ROOT}/LICENSE.txt"
cp release/server/NOTICE.txt "${PACKAGE_ROOT}/NOTICE.txt"
cp release/server/update-root-public-key.txt "${PACKAGE_ROOT}/UPDATE_ROOT_PUBLIC_KEY.txt"
cp release/server/THIRD_PARTY_NOTICES.md "${PACKAGE_ROOT}/THIRD_PARTY_NOTICES.md"
cp release/server/THIRD_PARTY_DISTRIBUTION_INVENTORY.md \
  "${PACKAGE_ROOT}/THIRD_PARTY_DISTRIBUTION_INVENTORY.md"

printf 'Generating the Server dependency inventory (SPDX)...\n'
node scripts/generate-server-sbom.mjs "${PACKAGE_ROOT}/THIRD_PARTY.spdx.json"

# Contract: bin/ and scripts/ keep the executable bit, everything else is 0644.
chmod 0755 "${PACKAGE_ROOT}/bin/visto-core" "${PACKAGE_ROOT}/bin/visto-server"
chmod 0755 "${PACKAGE_ROOT}/scripts/"*.sh
chmod 0644 "${PACKAGE_ROOT}/LICENSE.txt" "${PACKAGE_ROOT}/NOTICE.txt" \
  "${PACKAGE_ROOT}/UPDATE_ROOT_PUBLIC_KEY.txt" \
  "${PACKAGE_ROOT}/THIRD_PARTY_NOTICES.md" \
  "${PACKAGE_ROOT}/THIRD_PARTY_DISTRIBUTION_INVENTORY.md" \
  "${PACKAGE_ROOT}/THIRD_PARTY.spdx.json"

find "${PACKAGE_ROOT}" -name '.DS_Store' -delete

# --- archive ------------------------------------------------------------------

printf 'Creating %s...\n' "$(basename "${ARCHIVE}")"
# Members must sit at the archive root ("./bin/visto-core", not
# "Visto-Server_<version>_macos-arm64/bin/visto-core"): the contract requires
# bin/, web/ and scripts/ at the root, and visto_unix_validate_package only
# normalises a leading "./" - it does not strip a package directory. A leading
# directory would also violate the frozen path whitelist.
# COPYFILE_DISABLE stops bsdtar from adding AppleDouble ._ members, which the
# whitelist would reject anyway.
COPYFILE_DISABLE=1 tar -czf "${ARCHIVE}" -C "${PACKAGE_ROOT}" .

LISTING="${OUTPUT_DIR}/.candidate-entries.txt"
VERBOSE_LISTING="${OUTPUT_DIR}/.candidate-listing.txt"
tar -tzf "${ARCHIVE}" > "${LISTING}"
# Member types and the executable bit are enforced by the audit, from this
# listing, so the contract rule has one implementation rather than two.
tar -tvzf "${ARCHIVE}" > "${VERBOSE_LISTING}"

HASH="$(sha256_of "${ARCHIVE}")"
printf 'SHA256  %s\n%s\n' "$(basename "${ARCHIVE}")" "${HASH}" > "${ARCHIVE}.sha256"
printf '%s  %s\n' "${HASH}" "$(basename "${ARCHIVE}")" > "${OUTPUT_DIR}/checksums-${FILE_TOKEN}.txt"

# --- audit --------------------------------------------------------------------

if [ "${VISTO_CANDIDATE_AUDIT:-1}" != "0" ]; then
  printf 'Auditing the package against the frozen contract...\n'
  node scripts/release/audit-server-package.mjs \
    --archive "${ARCHIVE}" \
    --platform "${FILE_TOKEN}" \
    --version "${VERSION}" \
    --entries "${LISTING}" \
    --listing "${VERBOSE_LISTING}" \
    --sha256 "${HASH}" \
    --sha256-file "${ARCHIVE}.sha256" \
    --env-file scripts/server-unix/visto-server-env.sh \
    --report "${ARCHIVE}.audit.json"
fi

rm -f "${LISTING}" "${VERBOSE_LISTING}"
rm -rf "${PACKAGE_ROOT}"

printf '\nServer package: %s\n' "${ARCHIVE}"
printf 'SHA-256: %s\n' "${HASH}"
printf 'Checksums: %s\n' "${OUTPUT_DIR}/checksums-${FILE_TOKEN}.txt"
