#!/bin/sh
# Install the latest catchup release binary. No Go toolchain required.
# Invocation is documented in the README (https://github.com/wilbeibi/catchup).
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

# Resolve the latest tag once, then download everything from it: two separate
# fetches of releases/latest could straddle a release being published and fail
# the checksum comparison with artifacts from different releases.
tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")
tag=${tag##*/}
case "$tag" in
  v*) ;;
  *) echo "could not resolve the latest release tag (got ${tag:-nothing})" >&2; exit 1 ;;
esac

base="https://github.com/$REPO/releases/download/$tag"
archive="catchup_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -f "$tmp/$archive" "$tmp/checksums.txt" "$tmp/catchup"; rmdir "$tmp"' EXIT

echo "downloading $base/$archive"
curl -fsSL "$base/$archive" -o "$tmp/$archive"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

# Verify the archive against the release's published sha256 before unpacking.
want=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)
if [ -z "$want" ]; then
  echo "checksums.txt has no entry for $archive" >&2; exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)
fi
if [ "$want" != "$got" ]; then
  echo "checksum mismatch for $archive (expected $want, got $got)" >&2; exit 1
fi

tar -xzf "$tmp/$archive" -C "$tmp" catchup

mkdir -p "$DIR"
install -m 0755 "$tmp/catchup" "$DIR/catchup"
echo "installed $DIR/catchup ($tag)"

case ":$PATH:" in
  *":$DIR:"*) ;;
  *) echo "note: $DIR is not on your PATH" ;;
esac
