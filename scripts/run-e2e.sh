#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

if (( EUID != 0 )); then
  echo "[-] Run this target as root (for example: sudo make e2e)." >&2
  exit 1
fi

"$repo_root/scripts/verify-host.sh"

if [[ -z ${MINIDOCK_CGROUP_ROOT:-} ]]; then
  echo "[-] Set MINIDOCK_CGROUP_ROOT to a dedicated delegated cgroup v2 subtree." >&2
  exit 1
fi
cgroup_root=$(realpath -e -- "$MINIDOCK_CGROUP_ROOT")
if [[ $cgroup_root == /sys/fs/cgroup || ! -d $cgroup_root ]]; then
  echo "[-] MINIDOCK_CGROUP_ROOT must be a dedicated cgroup v2 subtree, not the hierarchy root." >&2
  exit 1
fi
export MINIDOCK_CGROUP_ROOT=$cgroup_root
if [[ ! -r $cgroup_root/cgroup.controllers || ! -w $cgroup_root/cgroup.procs || ! -r $cgroup_root/cgroup.subtree_control ]]; then
  echo "[-] The configured cgroup subtree is not readable/writable for this test." >&2
  exit 1
fi
controllers=$(< "$cgroup_root/cgroup.controllers")
subtree=$(< "$cgroup_root/cgroup.subtree_control")
for controller in cpu memory; do
  if [[ " $controllers " != *" $controller "* ]] || [[ " $subtree " != *" $controller "* && " $subtree " != *"+$controller "* ]]; then
    echo "[-] Enable and delegate the '$controller' controller in $cgroup_root (available: $controllers; enabled: $subtree)." >&2
    exit 1
  fi
done

# The runtime owns this interface and subnet during the test. Refuse to alter
# host networking that was configured by something else.
if ip link show dev minidock0 >/dev/null 2>&1; then
  echo "[-] Refusing to run: minidock0 already exists. Use a disposable host and remove or reconfigure it first." >&2
  exit 1
fi
if [[ -n $(ip -4 route show 10.0.0.0/24) ]]; then
  echo "[-] Refusing to run: 10.0.0.0/24 already has a host route." >&2
  exit 1
fi

cd "$repo_root/go-runtime"
go_bin=${MINIDOCK_GO_BIN:-$(command -v go || true)}
if [[ -z $go_bin && -x /usr/local/go/bin/go ]]; then
  go_bin=/usr/local/go/bin/go
fi
if [[ -z $go_bin || ! -x $go_bin ]]; then
  echo "[-] Go was not found. Install Go or set MINIDOCK_GO_BIN to its executable path." >&2
  exit 1
fi
GOCACHE=${GOCACHE:-/tmp/minidock-root-go-cache} \
  "$go_bin" test -tags=integration -count=1 -v ./internal/transport \
    -run '^TestPrivilegedRuntimeIntegration$'
