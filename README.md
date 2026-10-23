# Mini Docker

A Linux learning project split into a Java control plane and a Go runtime.
Java validates requests, stages images, extracts root filesystems and tracks
container state. Go creates namespaces, configures cgroups and networking,
enters the rootfs with `pivot_root`, and executes workloads.

## Start the services

Run both processes on the same Linux machine and as users that can access the
same Unix socket:

In terminal one:

```sh
cd go-runtime
go run ./cmd/minidockd
```

In terminal two:

```sh
cd java-control-plane
./mvnw spring-boot:run
```

Java serves its local API at `http://127.0.0.1:8080` by default. The Go socket
defaults to `/tmp/minidock-runtime.sock` and has mode `0660`. Override it with
`MINIDOCK_SOCKET` in both processes.

## Commands

`bin/minidock` provides a small CLI for the Java API:

```sh
bin/minidock build -t alpine-base alpine-minirootfs.tar.gz
bin/minidock images
bin/minidock run -it -m 256M -c 50 alpine-base /bin/sh
bin/minidock run -d --no-network alpine-base /bin/sh -c 'echo hello'
bin/minidock ps
bin/minidock stop CONTAINER_ID
bin/minidock logs CONTAINER_ID
```

`build` stages an existing `.tar` or `.tar.gz` rootfs archive; it does not
build a Dockerfile. `run` returns after Go confirms startup and reports the
container ID and host PID. Attached runs connect the caller's terminal to a
container PTY, including terminal resize and exit-code forwarding. Java exposes the same operations through
`/api/v1/images` and `/api/v1/containers`.

## Storage and host requirements

Image archives and extracted containers default to
`${user.home}/.local/share/minidock/{images,containers}`. Set
`MINIDOCK_IMAGES_DIR` or `MINIDOCK_CONTAINERS_DIR` to change those paths. Java
persists lifecycle snapshots atomically to `state.json`; override that path
with `MINIDOCK_STATE_FILE`. A sibling lock file prevents a second Java control
plane from writing the same state file. At startup Java reconciles saved records and IP
leases with the Go daemon's active-session list. Detached stdout/stderr is kept
in each container directory as `container.log`, capped at 10 MiB with a
truncation marker; logs remain after the rootfs is removed. Set
`MINIDOCK_CONTAINERS_DIR` for both Java and Go if they run with
different home directories.

The Go runtime requires Linux. Namespace creation and mounts depend on host
kernel policy. At daemon startup, MiniDock reports whether it is running as
root, whether cgroups v2 CPU/memory controllers are visible, and whether the
networking tools are installed. These checks are diagnostic: basic
no-network/no-limits containers may work as an ordinary user when the kernel
allows user namespaces. CPU and memory limits require a writable delegated
cgroups v2 hierarchy; set `MINIDOCK_CGROUP_ROOT` to its MiniDock subdirectory.
Container networking requires `iproute2`, `nsenter`, `iptables`, and permission
to create bridge/veth devices and change IPv4 forwarding. Missing tools are
reported with Debian/Ubuntu package hints at startup. MiniDock adds masquerade
and forwarding rules for `10.0.0.0/24`, then removes its rules and restores the
previous forwarding setting on normal daemon shutdown.

### Host compatibility

MiniDock is Linux-only. The real namespace, mount, cgroup, bridge/veth, and
firewall path has passed once on the project's Zorin OS development host; its
exact release and kernel version were not recorded. Ubuntu, Debian, Fedora, and
other distributions have not been verified yet. A successful preflight does
not guarantee that the host permits namespace creation, mounts, cgroup
delegation, or network administration.

AppArmor or SELinux policy can deny these operations even when the daemon is
root. If startup reports `operation not permitted`, inspect the system journal
and the relevant security audit logs for a policy denial. Apply a narrow local
policy adjustment for the MiniDock executable and required operations; do not
disable SELinux or put the host's AppArmor profile into complain mode as a
general workaround.

The Java-to-Go contract is protobuf gRPC over the local Unix socket. Go's
`Start`, `Wait`, `Stop`, `ListActiveContainers`, and `GetContainerLogs`
operations let Java acknowledge startup with a PID, wait for exit status,
stop workloads, reconcile state, and retrieve bounded log ranges. Run
`make test-integration` runs the privileged host preflight and the existing Go
integration checks. For real namespace, mount, cgroups v2, bridge/veth, and
firewall setup, use `make e2e` on a disposable Linux host with a delegated
cgroup v2 subtree configured in `MINIDOCK_CGROUP_ROOT`. The e2e target refuses
to run if `minidock0` or a route for `10.0.0.0/24` already exists.
