#!/bin/sh
# Install a checksum-pinned official GitHub CLI into ignored local build output.
set -eu
[ "$#" -eq 0 ] || { echo 'usage: sh scripts/install-release-gh.sh' >&2; exit 2; }
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
case "$(uname -s)/$(uname -m)" in
    Linux/x86_64) arch=amd64; checksum=62ccf747115ddc61237efffdde66a3c8f70ba249433dc64f70801089b59c59c8 ;;
    Linux/aarch64|Linux/arm64) arch=arm64; checksum=4c3e99a50bd8b181a3fe9d69b25dd978bac768148d88f01507c6b4d1ef61a443 ;;
    *) echo 'release CLI installer requires Linux amd64 or arm64' >&2; exit 1 ;;
esac
mkdir -p build/release-tools
stage=$(mktemp -d build/release-tools/.install.XXXXXX)
trap 'rm -rf "$stage"' EXIT
trap 'exit 1' HUP INT TERM
archive="gh_2.103.2_linux_${arch}"
echo 'Downloading official GitHub CLI v2.103.2'
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    --connect-timeout 10 --max-time 120 --retry 2 \
    "https://github.com/cli/cli/releases/download/v2.103.2/$archive.tar.gz" -o "$stage/gh.tar.gz"
printf '%s  %s\n' "$checksum" "$stage/gh.tar.gz" | shasum -a 256 -c -
tar -xOf "$stage/gh.tar.gz" "$archive/bin/gh" > "$stage/gh"
chmod 755 "$stage/gh"
mv "$stage/gh" build/release-tools/gh
build/release-tools/gh --version
