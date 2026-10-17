# MiniDock Go runtime

Go is the low-level Linux jailer. Java owns the REST API, image extraction,
container registry, and lifecycle state. The Go runtime keeps no container
registry and does not extract image archives.

## Request flow

The Java control plane calls the `Run` method on
`minidock.runtime.v1.RuntimeService` over the Unix socket
`/tmp/minidock-runtime.sock` (override with `MINIDOCK_SOCKET`). The socket is
created with mode `0660`. Messages use gRPC's JSON content subtype; the request
contains a prepared absolute `rootfs` path, command, environment, hostname,
optional CPU and memory limits, and an optional `network` flag. The RPC waits
for the workload to exit. Its
context cancellation kills the child process, and the response carries the
exit code. `protocol/runtime.proto` is reserved for a future protobuf wire
contract and is not used by this JSON transport yet.

## Kernel path

1. `container` validates the request and coordinates setup and cleanup.
2. `isolation` re-executes the runtime binary with new user, PID, mount,
   network, UTS, and IPC namespaces. The child waits while the parent applies
   requested resource limits.
3. `cgroups` creates a cgroups v2 group, writes `memory.max` and/or `cpu.max`,
   then writes the child PID to `cgroup.procs`. The group is removed when the
   workload exits. Set `MINIDOCK_CGROUP_ROOT` to use a delegated cgroup root;
   the default is `/sys/fs/cgroup/minidock`.
4. When `network` is requested, the host manager creates `minidock0` at
   `10.0.0.1/24`, allocates a container address, and connects a veth to the
   child namespace. This requires `ip`, `nsenter`, and host `CAP_NET_ADMIN`.
   The child brings loopback up through a Linux ioctl, enters the prepared
   rootfs with a private mount tree, mounts `/proc` for its PID namespace,
   calls `pivot_root`, detaches the old root, then replaces itself with the
   requested command. A denied `/proc` mount fails container startup.

The rootfs must already be safely prepared by Java. Go does not unpack images
or manage container state. Networking is opt-in per request and provides a
bridge, veth, container address, and default route; host forwarding and NAT are
not configured. The runtime requires permission to mount `/proc`; setup errors
fail the launch instead of starting a container with an incomplete process
filesystem.

## Packages

- `cmd/minidockd`: local gRPC socket and hidden child-process entry point.
- `internal/transport`: the stateless Java-to-Go `Run` handshake.
- `internal/container`: launch config validation and ordered lifecycle.
- `internal/isolation`: namespace creation and re-exec handshake.
- `internal/rootfs`: `pivot_root` and `/proc` setup.
- `internal/cgroups`: cgroups v2 resource limits.
- `internal/network`: loopback and optional host bridge/veth setup.

Run the end-to-end Go test and package checks from this directory with
`go test ./...` and `go vet ./...`. The integration test needs Linux user,
PID, mount, and network namespaces plus `busybox`; it skips when `busybox` is
not installed or rootless namespace creation is prohibited.
