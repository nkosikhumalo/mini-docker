#!/usr/bin/env bash
set -euo pipefail

if (( EUID != 0 )); then
  echo "[-] Run this check as root (for example: sudo scripts/verify-host.sh)." >&2
  exit 1
fi

if [[ ! -r /sys/fs/cgroup/cgroup.controllers ]]; then
  echo "[-] cgroup v2 is unavailable at /sys/fs/cgroup/cgroup.controllers." >&2
  exit 1
fi

controllers=$(< /sys/fs/cgroup/cgroup.controllers)
for controller in cpu memory; do
  if [[ " $controllers " != *" $controller "* ]]; then
    echo "[-] cgroup v2 controller '$controller' is unavailable; controllers: $controllers" >&2
    exit 1
  fi
done

for command_name in ip nsenter iptables; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "[-] Required command '$command_name' is not installed." >&2
    exit 1
  fi
done

if [[ ! -r /proc/sys/net/ipv4/ip_forward || ! -w /proc/sys/net/ipv4/ip_forward ]]; then
  echo "[-] IPv4 forwarding cannot be inspected and changed by this process." >&2
  exit 1
fi

echo "[+] Host has cgroup v2 CPU/memory controllers and required network tools."
echo "[!] Namespace, mount, cgroup delegation, and CAP_NET_ADMIN checks run during integration tests."
