#!/usr/bin/env sh
# Install SIIASQL MCP from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/ofepbolivia/siia-mcp/main/scripts/install.sh | sh
#
# Environment:
#   SIIASQL_VERSION         Release tag to install (default: latest)
#   INSTALL_DIR             Destination directory (default: /usr/local/bin, fallback: ~/.local/bin)
#   GITHUB_REPOSITORY       Override repository owner/name (default: ofepbolivia/siia-mcp)
#   SIIASQL_DOWNLOAD_BASE   Override release asset base URL for mirrors/tests
set -eu

repo="${GITHUB_REPOSITORY:-ofepbolivia/siia-mcp}"
version="${SIIASQL_VERSION:-latest}"
install_dir="${INSTALL_DIR:-/usr/local/bin}"

need() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "error: required command not found: $1" >&2
        exit 1
    fi
}

need uname
need mktemp
need tar

if command -v sha256sum >/dev/null 2>&1; then
    checksum_tool="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
    checksum_tool="shasum"
else
    echo "error: sha256sum or shasum is required" >&2
    exit 1
fi

if command -v curl >/dev/null 2>&1; then
    fetch='curl -fsSL'
elif command -v wget >/dev/null 2>&1; then
    fetch='wget -qO-'
else
    echo "error: curl or wget is required" >&2
    exit 1
fi

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in
    linux) os="linux" ;;
    darwin) os="darwin" ;;
    *) echo "error: unsupported OS: $os" >&2; exit 1 ;;
esac

case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *) echo "error: unsupported architecture: $arch" >&2; exit 1 ;;
esac

if [ -n "${SIIASQL_DOWNLOAD_BASE:-}" ]; then
    base="${SIIASQL_DOWNLOAD_BASE%/}"
elif [ "$version" = "latest" ]; then
    base="https://github.com/${repo}/releases/latest/download"
else
    base="https://github.com/${repo}/releases/download/${version}"
fi

name="siiasql_${os}_${arch}"
archive="${name}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "downloading ${repo} ${version} for ${os}/${arch}..." >&2
$fetch "${base}/${archive}" > "${tmp}/${archive}"
$fetch "${base}/checksums.txt" > "${tmp}/checksums.txt"

(
    cd "$tmp"
    line="$(grep "  ${archive}$" checksums.txt || true)"
    if [ -z "$line" ]; then
        echo "error: checksum not found for ${archive}" >&2
        exit 1
    fi
    set -- $line
    expected="$1"
    if [ "$checksum_tool" = "sha256sum" ]; then
        set -- $(sha256sum "$archive")
    else
        set -- $(shasum -a 256 "$archive")
    fi
    actual="$1"
    if [ "$expected" != "$actual" ]; then
        echo "error: checksum mismatch for ${archive}" >&2
        exit 1
    fi
) >/dev/null
tar -xzf "${tmp}/${archive}" -C "$tmp"

if [ ! -d "$install_dir" ] || [ ! -w "$install_dir" ]; then
    fallback="${HOME}/.local/bin"
    mkdir -p "$fallback"
    install_dir="$fallback"
fi

cp "${tmp}/siiasql" "${install_dir}/siiasql"
chmod 755 "${install_dir}/siiasql"

echo "siiasql installed to ${install_dir}/siiasql" >&2
if ! command -v siiasql >/dev/null 2>&1; then
    echo "note: ${install_dir} is not on PATH; add it before running siiasql" >&2
fi
