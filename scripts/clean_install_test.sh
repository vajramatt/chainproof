#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/chainproof-clean-install.XXXXXX")
server_pid=
cleanup() {
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$test_root"
}
trap cleanup EXIT INT TERM

version=v0.0.0-clean-install
binary_version=${version#v}
case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "clean install checks support macOS and Linux" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) echo "unsupported clean install architecture: $(uname -m)" >&2; exit 1 ;;
esac

archive="chainproof_${version}_${os}_${arch}.tar.gz"
payload="$test_root/payload/${archive%.tar.gz}"
mkdir -p "$payload" "$test_root/fakebin" "$test_root/home" "$test_root/work"

CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
  -ldflags="-s -w -X main.version=$binary_version" \
  -o "$payload/chainproof" "$repo_root/cmd/chainproof"
cp "$repo_root/LICENSE" "$repo_root/README.md" "$payload/"
tar -C "$test_root/payload" -czf "$test_root/$archive" "${archive%.tar.gz}"
if command -v sha256sum >/dev/null 2>&1; then
  archive_sum=$(sha256sum "$test_root/$archive" | awk '{print $1}')
else
  archive_sum=$(shasum -a 256 "$test_root/$archive" | awk '{print $1}')
fi
printf '%s  %s\n' "$archive_sum" "$archive" >"$test_root/checksums.txt"

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

cat >"$test_root/fakebin/launchctl" <<'EOF'
#!/bin/sh
printf 'launchctl %s\n' "$*" >>"$CHAINPROOF_TEST_SERVICE_LOG"
case "${1:-}" in
  print) printf '%s\n' 'chainproof test service: running' ;;
esac
EOF

cat >"$test_root/fakebin/systemctl" <<'EOF'
#!/bin/sh
printf 'systemctl %s\n' "$*" >>"$CHAINPROOF_TEST_SERVICE_LOG"
case " $* " in
  *' status '*) printf '%s\n' 'chainproof test service: running' ;;
esac
EOF
chmod 0755 "$test_root/fakebin/curl" "$test_root/fakebin/launchctl" "$test_root/fakebin/systemctl"

real_curl=$(command -v curl)
export HOME="$test_root/home"
export TMPDIR="$test_root/work"
export BINDIR="$test_root/home/.local/bin"
export CHAINPROOF_VERSION="$version"
export CHAINPROOF_TEST_ARCHIVE="$test_root/$archive"
export CHAINPROOF_TEST_CHECKSUMS="$test_root/checksums.txt"
export CHAINPROOF_TEST_SERVICE_LOG="$test_root/service.log"
export CHAINPROOF_CODEX_DISABLED=1
export CHAINPROOF_AGENT_PROFILE=clean-install-agent
export CHAINPROOF_COLOR=never
export PATH="$test_root/fakebin:$BINDIR:$PATH"

sh "$repo_root/scripts/install.sh" >"$test_root/install.log"
binary="$BINDIR/chainproof"
test -x "$binary"
"$binary" version | grep -F "$binary_version" >/dev/null

"$binary" capabilities --json >"$test_root/capabilities.json"
grep -F '"product": "chainproof"' "$test_root/capabilities.json" >/dev/null
if [ -e "$HOME/.chainproof" ]; then
  echo "capability discovery created local state" >&2
  exit 1
fi

"$binary" init --json >"$test_root/init.json"
grep -F '"status": "ready"' "$test_root/init.json" >/dev/null
"$binary" doctor --json >"$test_root/doctor.json"
grep -F '"status": "ready"' "$test_root/doctor.json" >/dev/null

"$binary" whoami >"$test_root/identity-one.json"
"$binary" agent ensure --profile clean-install-agent --name CleanInstall --harness smoke >"$test_root/identity-two.json"
agent_one=$(sed -n 's/.*"agent_id": "\([^"]*\)".*/\1/p' "$test_root/identity-one.json" | head -n 1)
agent_two=$(sed -n 's/.*"agent_id": "\([^"]*\)".*/\1/p' "$test_root/identity-two.json" | head -n 1)
if [ -z "$agent_one" ] || [ "$agent_one" != "$agent_two" ]; then
  echo "stable agent identity changed during clean bootstrap" >&2
  exit 1
fi

"$binary" mission start --objective "Verify clean autonomous lifecycle" --role verifier >"$test_root/mission.json"
mission_id=$(sed -n 's/.*"mission_id": "\([^"]*\)".*/\1/p' "$test_root/mission.json" | head -n 1)
"$binary" start --agent CleanInstall --harness smoke --model release-test --mission "$mission_id" --role verifier >"$test_root/run.json"
run_id=$(sed -n 's/.*"run_id": "\([^"]*\)".*/\1/p' "$test_root/run.json" | head -n 1)
if [ -z "$mission_id" ] || [ -z "$run_id" ]; then
  echo "clean lifecycle did not return mission and run identifiers" >&2
  exit 1
fi
"$binary" append "$run_id" '{"kind":"clean.install.verified","payload":{"archive":true,"identity":true}}' >"$test_root/event.json"
event_id=$(sed -n 's/.*"event_id": "\([^"]*\)".*/\1/p' "$test_root/event.json" | head -n 1)
"$binary" checkpoint "$mission_id" "$run_id" "{\"summary\":\"Clean install lifecycle works\",\"next_actions\":[],\"blockers\":[],\"evidence\":[{\"event_id\":\"$event_id\",\"note\":\"installed binary wrote and verified evidence\"}]}" >"$test_root/checkpoint.json"
"$binary" complete "$run_id" completed >"$test_root/completed-run.json"
"$binary" mission complete "$mission_id" >"$test_root/completed-mission.json"
"$binary" verify "$run_id" >"$test_root/verify.json"
grep -F '"valid": true' "$test_root/verify.json" >/dev/null
"$binary" mission export "$mission_id" "$test_root/mission-proof.json"
"$binary" verify-continuity-file "$test_root/mission-proof.json" >"$test_root/verify-continuity.json"
grep -F '"valid": true' "$test_root/verify-continuity.json" >/dev/null

"$binary" backup "$test_root/backup" >"$test_root/backup.json"
"$binary" restore "$test_root/backup" "$test_root/restored" >"$test_root/restore.json"
CHAINPROOF_DB="$test_root/restored/chainproof.db" \
CHAINPROOF_AGENT_HOME="$test_root/restored/agents" \
  "$binary" doctor --json >"$test_root/restored-doctor.json"
grep -F '"status": "ready"' "$test_root/restored-doctor.json" >/dev/null

address=127.0.0.1:17331
"$binary" serve "$address" >"$test_root/server.log" 2>&1 &
server_pid=$!
ready=
attempt=0
while [ "$attempt" -lt 50 ]; do
  if "$real_curl" -fsS "http://$address/api/status" >"$test_root/status.json" 2>/dev/null; then
    ready=1
    break
  fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    break
  fi
  attempt=$((attempt + 1))
  sleep 0.1
done
if [ -z "$ready" ]; then
  cat "$test_root/server.log" >&2
  echo "loopback explorer did not become ready" >&2
  exit 1
fi
"$real_curl" -fsS "http://$address/" >"$test_root/explorer.html"
grep -F 'ChainProof' "$test_root/explorer.html" >/dev/null
kill "$server_pid"
wait "$server_pid" 2>/dev/null || true
server_pid=

case "$os" in
  darwin) printf q | script -q /dev/null "$binary" ui >"$test_root/tui.log" ;;
  linux) printf q | script -q -c "$binary ui" /dev/null >"$test_root/tui.log" ;;
esac
grep -F 'Starting ChainProof' "$test_root/tui.log" >/dev/null

: >"$test_root/service.log"
"$binary" service install >"$test_root/service-install.log"
case "$os" in
  darwin) service_config="$HOME/Library/LaunchAgents/dev.chainproof.daemon.plist" ;;
  linux) service_config="$HOME/.config/systemd/user/chainproof.service" ;;
esac
test -f "$service_config"
"$binary" service status >"$test_root/service-status.log"
grep -F 'chainproof test service: running' "$test_root/service-status.log" >/dev/null
"$binary" service uninstall >"$test_root/service-uninstall.log"
if [ -e "$service_config" ]; then
  echo "service uninstall left supervisor configuration behind" >&2
  exit 1
fi
test -f "$HOME/.chainproof/chainproof.db"
test -f "$HOME/.chainproof/agents/clean-install-agent/profile.json"
test -f "$HOME/.chainproof/agents/clean-install-agent/identity.key"

echo "clean install lifecycle checks passed ($os/$arch)"
