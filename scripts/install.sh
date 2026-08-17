#!/bin/sh
set -eu

version="${CHAINPROOF_VERSION:-v0.3.0}"
repo="https://github.com/vajramatt/chainproof"
bindir="${BINDIR:-${HOME}/.local/bin}"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "ChainProof supports macOS and Linux." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

archive="chainproof_${version}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/chainproof.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "downloading ChainProof $version for $os/$arch"
curl -fsSL "$repo/releases/download/$version/$archive" -o "$tmp/$archive"
curl -fsSL "$repo/releases/download/$version/checksums.txt" -o "$tmp/checksums.txt"
expected=$(awk -v file="$archive" '$2 == file { print $1 }' "$tmp/checksums.txt")
if [ -z "$expected" ]; then echo "checksum not found for $archive" >&2; exit 1; fi
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$tmp/$archive" | awk '{print $1}'); else actual=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}'); fi
if [ "$actual" != "$expected" ]; then echo "checksum verification failed" >&2; exit 1; fi

tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$bindir"
install -m 0755 "$tmp/${archive%.tar.gz}/chainproof" "$bindir/chainproof"

echo "installed $bindir/chainproof"
case ":$PATH:" in *":$bindir:"*) ;; *) echo "add $bindir to your PATH" ;; esac
echo "run: chainproof"
echo "keep it running: chainproof service install"
