#!/bin/sh
set -eu

version="${1:-$(git describe --tags --exact-match 2>/dev/null || true)}"
if [ -z "$version" ]; then
  echo "usage: scripts/build-release.sh v0.5.0" >&2
  exit 2
fi

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
dist="$root/dist"
mkdir -p "$dist"

for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do
  os=${target%/*}
  arch=${target#*/}
  name="chainproof_${version}_${os}_${arch}"
  work="$dist/$name"
  mkdir -p "$work"
  echo "building $os/$arch"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags="-s -w" -o "$work/chainproof" ./cmd/chainproof
  cp LICENSE README.md "$work/"
  tar -C "$dist" -czf "$dist/$name.tar.gz" "$name"
  rm -r "$work"
done

cd "$dist"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum chainproof_"$version"_*.tar.gz > checksums.txt
else
  shasum -a 256 chainproof_"$version"_*.tar.gz > checksums.txt
fi
echo "release assets written to $dist"
