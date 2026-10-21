# MiniDock Go runtime

Go is the Linux jailer. Java owns the HTTP API, image storage/extraction, and
container registry. The runtime receives prepared rootfs paths over a local
protobuf gRPC Unix socket and manages only active kernel processes.

## Runtime protocol

The daemon listens on `/tmp/minidock-runtime.sock` (override with
`MINIDOCK_SOCKET`) and creates the socket with mode `0660`. `Start` validates a
request, prepares namespaces/cgroups/network/rootfs, and returns the host PID
once child setup is acknowledged. `Wait` returns the process exit code and
`Stop` cancels it. On daemon shutdown, active child processes are canceled and
waited for before the socket server exits.

## Kernel path

1. `container` validates configuration and orders setup/cleanup.
2. `isolation` re-executes the runtime in new user, PID, mount, network, UTS,
   and IPC namespaces. The parent holds the child while applying cgroups and
   optional networking.
3. `cgroups` writes requested memory/CPU controls and registers the child PID.
   Configure a delegated cgroups v2 subtree with `MINIDOCK_CGROUP_ROOT`.
4. `network` creates `minidock0` at `10.0.0.1/24`, assigns a container IP,
   attaches a veth, and configures the container default route. This requires
   `ip`, `nsenter`, `iptables`, `CAP_NET_ADMIN`, and permission to change IPv4
   forwarding. It installs idempotent masquerade and forwarding rules, then
   removes rules it owns and restores the prior forwarding setting on normal
   daemon shutdown.
5. The namespace child brings loopback up, bind-mounts rootfs, mounts `/proc`,
   calls `pivot_root`, detaches the old root, acknowledges setup, and execs the
   command. A failed `/proc` mount fails startup.

For `attach` requests, the runtime creates a PTY, makes it the child's
controlling terminal, and exposes a bidirectional gRPC stream for terminal
input/output, window-size updates, and the final exit code. The CLI connects to
that stream after receiving the start acknowledgement.

The runtime does not extract images or persist state. Non-attached child stdio
inherits the daemon's streams. Kernel capabilities and cgroup delegation must
be provisioned by the host. Forwarding settings are restored on normal daemon
shutdown; a forced shutdown can leave host firewall rules or forwarding
enabled and should be followed by host-level cleanup.

## Packages and verification

- `cmd/minidockd`: gRPC socket and namespace child entry point.
- `internal/transport`: Java-to-Go RPC boundary and session handling.
- `internal/container`: configuration validation and lifecycle.
- `internal/isolation`: namespaces and re-exec handshake.
- `internal/rootfs`: procfs and `pivot_root` setup.
- `internal/cgroups`: cgroups v2 limits.
- `internal/network`: loopback and bridge/veth management.

Run `go test ./...` and `go vet ./...` from this directory. The kernel test
needs Linux namespace/mount permission and `busybox`; it skips when the host
does not permit those operations.
