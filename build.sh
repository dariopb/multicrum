#!/usr/bin/env bash
# Build statically linked multicrum binaries for Linux and Windows.
#
# Usage:
#   ./build.sh                # build linux-amd64 + windows-amd64 (default)
#   ./build.sh linux          # only linux-amd64
#   ./build.sh windows        # only windows-amd64
#   ./build.sh all            # linux + windows, both amd64 + arm64
#
# Output goes to ./dist/multicrum-<os>-<arch>[.exe].

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
PKG="./cmd/multicrum/"

# Embed VCS info (commit + dirty flag) so `multicrum --version` style tools
# and Go's debug.ReadBuildInfo can report the source revision.
LDFLAGS="-s -w"

mkdir -p "${DIST_DIR}"

build_one() {
    local goos="$1" goarch="$2"
    local ext="" name="multicrum-${goos}-${goarch}"
    if [[ "${goos}" == "windows" ]]; then
        ext=".exe"
    fi
    local out="${DIST_DIR}/${name}${ext}"

    echo ">> building ${name}${ext}"
    # CGO_ENABLED=0 forces a fully static binary on Linux (no glibc/musl
    # linkage) and a self-contained PE on Windows (no MSVCRT-CGO shim).
    CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
        go build -trimpath -ldflags "${LDFLAGS}" -o "${out}" "${PKG}"

    # Sanity check: confirm the linux binary really is static.
    if [[ "${goos}" == "linux" ]] && command -v file >/dev/null 2>&1; then
        if ! file "${out}" | grep -qE 'statically linked|static-pie linked'; then
            echo "!! warning: ${out} does not look statically linked:" >&2
            file "${out}" >&2
        fi
    fi

    # Provide convenience copies under the bare names for the primary
    # amd64 builds, so users can run ./dist/multicrum or
    # ./dist/multicrum.exe without typing the full os-arch suffix.
    if [[ "${goarch}" == "amd64" ]]; then
        cp -f "${out}" "${DIST_DIR}/multicrum${ext}"
    fi
}

target="${1:-default}"
case "${target}" in
    default)
        build_one linux   amd64
        build_one windows amd64
        ;;
    linux)
        build_one linux amd64
        ;;
    windows|win)
        build_one windows amd64
        ;;
    all)
        build_one linux   amd64
        build_one linux   arm64
        build_one windows amd64
        build_one windows arm64
        ;;
    *)
        echo "unknown target: ${target}" >&2
        echo "usage: $0 [default|linux|windows|all]" >&2
        exit 2
        ;;
esac

echo
echo "artifacts in ${DIST_DIR}:"
ls -lh "${DIST_DIR}"
