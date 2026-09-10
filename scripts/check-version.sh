#!/bin/sh
set -eu

work=$(mktemp -d "${TMPDIR:-/tmp}/chainproof-version.XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM

go build -trimpath -ldflags="-X main.version=9.9.9-test" -o "$work/chainproof" ./cmd/chainproof
actual=$($work/chainproof version)
if [ "$actual" != "chainproof 9.9.9-test" ]; then
  echo "version injection failed: $actual" >&2
  exit 1
fi
