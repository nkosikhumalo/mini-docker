#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
if (( EUID != 0 )); then
  echo "[-] Run this target as root (for example: sudo make e2e-recovery)." >&2
  exit 1
fi

for tool in ip iptables; do
  command -v "$tool" >/dev/null || { echo "[-] Required command '$tool' was not found." >&2; exit 1; }
done
if [[ ! -r /proc/sys/net/ipv4/ip_forward || ! -w /proc/sys/net/ipv4/ip_forward ]]; then
  echo "[-] IPv4 forwarding cannot be inspected and changed by this process." >&2
  exit 1
fi
if ip link show dev minidock0 >/dev/null 2>&1; then
  echo "[-] Refusing to run: minidock0 already exists. Use a disposable Linux host." >&2
  exit 1
fi
if [[ -n $(ip -4 route show 10.0.0.0/24) ]]; then
  echo "[-] Refusing to run: 10.0.0.0/24 already has a host route." >&2
  exit 1
fi

go_bin=${MINIDOCK_GO_BIN:-$(command -v go || true)}
if [[ -z $go_bin && -x /usr/local/go/bin/go ]]; then
  go_bin=/usr/local/go/bin/go
fi
if [[ -z $go_bin || ! -x $go_bin ]]; then
  echo "[-] Go was not found. Install Go or set MINIDOCK_GO_BIN to its executable path." >&2
  exit 1
fi

state_dir=$(mktemp -d "${TMPDIR:-/tmp}/minidock-recovery-e2e.XXXXXX")
trap 'rmdir "$state_dir" 2>/dev/null || true' EXIT
export MINIDOCK_NETWORK_STATE_FILE="$state_dir/network-state.json"
cd "$repo_root/go-runtime"
GOCACHE=${GOCACHE:-/tmp/minidock-recovery-go-cache} \
  "$go_bin" test -tags=integration -count=1 -v ./internal/network \
    -run '^TestRecoveryAfterForcedDaemonTermination$'
