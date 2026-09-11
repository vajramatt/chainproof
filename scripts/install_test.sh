#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/chainproof-install-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT INT TERM

version=v9.9.9
os=linux
arch=amd64
archive="chainproof_${version}_${os}_${arch}.tar.gz"
payload="$test_root/payload/${archive%.tar.gz}"
mkdir -p "$payload" "$test_root/fakebin" "$test_root/bin" "$test_root/work" "$test_root/home/.chainproof"

cat >"$payload/chainproof" <<'EOF'
#!/bin/sh
printf '%s\n' new
EOF
chmod 0755 "$payload/chainproof"
tar -C "$test_root/payload" -czf "$test_root/$archive" "${archive%.tar.gz}"
if command -v sha256sum >/dev/null 2>&1; then
	archive_sum=$(sha256sum "$test_root/$archive" | awk '{print $1}')
else
	archive_sum=$(shasum -a 256 "$test_root/$archive" | awk '{print $1}')
fi
printf '%s  %s\n' "$archive_sum" "$archive" >"$test_root/checksums.txt"
printf '%s  %s\n' "0000000000000000000000000000000000000000000000000000000000000000" "$archive" >"$test_root/bad-checksums.txt"

cat >"$test_root/fakebin/uname" <<'EOF'
#!/bin/sh
case "${1:-}" in
	-s) printf '%s\n' Linux ;;
	-m) printf '%s\n' x86_64 ;;
	*) exit 2 ;;
esac
EOF

cat >"$test_root/fakebin/curl" <<'EOF'
#!/bin/sh
url=
output=
while [ "$#" -gt 0 ]; do
	case "$1" in
		-o) shift; output=$1 ;;
		-*) ;;
		*) url=$1 ;;
	esac
	shift
done
case "$url" in
	*/checksums.txt) source=$CHAINPROOF_TEST_CHECKSUMS ;;
	*) source=$CHAINPROOF_TEST_ARCHIVE ;;
esac
cp "$source" "$output"
EOF

cat >"$test_root/fakebin/install" <<'EOF'
#!/bin/sh
source=
destination=
while [ "$#" -gt 0 ]; do
	case "$1" in
		-m) shift ;;
		-*) ;;
		*) if [ -z "$source" ]; then source=$1; else destination=$1; fi ;;
	esac
shift
done
if [ "$destination" = "$CHAINPROOF_TEST_FINAL" ]; then
	echo "installer overwrote live destination directly" >&2
	exit 91
fi
cp "$source" "$destination"
chmod 0755 "$destination"
EOF
chmod 0755 "$test_root/fakebin/uname" "$test_root/fakebin/curl" "$test_root/fakebin/install"

cat >"$test_root/bin/chainproof" <<'EOF'
#!/bin/sh
printf '%s\n' old
EOF
chmod 0755 "$test_root/bin/chainproof"
printf '%s\n' preserved >"$test_root/home/.chainproof/chainproof.db"

PATH="$test_root/fakebin:$PATH" \
	HOME="$test_root/home" \
	TMPDIR="$test_root/work" \
	BINDIR="$test_root/bin" \
	CHAINPROOF_VERSION="$version" \
	CHAINPROOF_TEST_ARCHIVE="$test_root/$archive" \
	CHAINPROOF_TEST_CHECKSUMS="$test_root/checksums.txt" \
	CHAINPROOF_TEST_FINAL="$test_root/bin/chainproof" \
	sh "$repo_root/scripts/install.sh" >/dev/null

if [ "$("$test_root/bin/chainproof")" != "new" ]; then
	echo "new binary was not installed" >&2
	exit 1
fi
if [ "$(cat "$test_root/home/.chainproof/chainproof.db")" != "preserved" ]; then
	echo "installer changed existing state" >&2
	exit 1
fi

cat >"$test_root/bin/chainproof" <<'EOF'
#!/bin/sh
printf '%s\n' old
EOF
chmod 0755 "$test_root/bin/chainproof"
if PATH="$test_root/fakebin:$PATH" \
	HOME="$test_root/home" \
	TMPDIR="$test_root/work" \
	BINDIR="$test_root/bin" \
	CHAINPROOF_VERSION="$version" \
	CHAINPROOF_TEST_ARCHIVE="$test_root/$archive" \
	CHAINPROOF_TEST_CHECKSUMS="$test_root/bad-checksums.txt" \
	CHAINPROOF_TEST_FINAL="$test_root/bin/chainproof" \
	sh "$repo_root/scripts/install.sh" >/dev/null 2>&1; then
	echo "installer accepted invalid checksum" >&2
	exit 1
fi
if [ "$("$test_root/bin/chainproof")" != "old" ]; then
	echo "failed install changed existing binary" >&2
	exit 1
fi

echo "installer upgrade checks passed"
