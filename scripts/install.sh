#!/bin/sh
# Install the latest catchup release binary. No Go toolchain required.
#   curl -fsSL https://raw.githubusercontent.com/wilbeibi/catchup/main/scripts/install.sh | sh
# Installs to ~/.local/bin (override with CATCHUP_INSTALL_DIR).
set -eu

REPO="wilbeibi/catchup"
DIR="${CATCHUP_INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) echo "unsupported OS: $os (download from https://github.com/$REPO/releases)" >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

url="https://github.com/$REPO/releases/latest/download/catchup_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "downloading $url"
curl -fsSL "$url" -o "$tmp/catchup.tar.gz"
tar -xzf "$tmp/catchup.tar.gz" -C "$tmp" catchup

mkdir -p "$DIR"
install -m 0755 "$tmp/catchup" "$DIR/catchup"
echo "installed $DIR/catchup ($("$DIR/catchup" --version))"

case ":$PATH:" in
  *":$DIR:"*) ;;
  *) echo "note: $DIR is not on your PATH" ;;
esac
