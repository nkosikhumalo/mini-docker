# MiniDock System Handover

This document is an orientation guide for the next person working on MiniDock. It explains what the project currently does, how its parts communicate, how to run it, where important behavior lives, and which gaps still need attention. Treat the source code and `protocol/runtime.proto` as authoritative if this document and implementation ever disagree.

## 1. What MiniDock is

MiniDock is a deliberately small Linux learning project: it stages a rootfs tarball and runs a command from it with basic process, filesystem, resource, and optional network isolation. It is not intended to reproduce Docker's full product surface or internal architecture. It has two main programs:

- **Java control plane** (`java-control-plane`): accepts HTTP requests and CLI calls, validates container requests, stores/extracts images, assigns container IDs and IPs, persists lifecycle metadata, and asks Go to run/stop containers.
- **Go runtime daemon** (`go-runtime`, executable `minidockd`): receives local gRPC requests, creates Linux isolation and resource controls, launches the command, and reports its PID, exit status, and logs.

The Java API defaults to `127.0.0.1:8080`. Java and Go communicate over a Unix domain socket, default `/tmp/minidock-runtime.sock`, using protobuf gRPC. Keep new work proportional to this project's mini scope: implement what the documented local workflow needs, and avoid Docker-compatible features or complexity unless the project explicitly chooses to expand.

## 2. Repository map

| Path | Responsibility |
| --- | --- |
| `bin/minidock` | Bash CLI for image and container HTTP endpoints, including attach and log retrieval. |
| `Makefile` | Build, test, run, and privileged host integration targets. |
| `protocol/runtime.proto` | Shared Java-Go RPC and message contract. |
| `go-runtime/cmd/minidockd` | Daemon startup, socket setup, runtime service wiring, shutdown. |
| `go-runtime/cmd/minidock` | Namespace child entrypoint and attach client executable. |
| `go-runtime/internal/transport` | gRPC server/client, active sessions, attach streams, lifecycle, log RPC. |
| `go-runtime/internal/container` | Runtime config validation and process lifecycle orchestration. |
| `go-runtime/internal/isolation` | Re-exec, namespaces, child setup, PTY handling. |
| `go-runtime/internal/rootfs` | Mount preparation, minimal `/dev`, `/proc`, `pivot_root`, old-root cleanup. |
| `go-runtime/internal/cgroups` | cgroups v2 directory and CPU/memory control configuration. |
| `go-runtime/internal/network` | Bridge, veth, container address/route, forwarding and masquerade. |
| `go-runtime/gen/runtime/v1` | Generated Go protobuf and gRPC bindings; do not hand-edit. |
| `java-control-plane/src/main/java/dev/minidocker/api` | HTTP controllers and API request/response records. |
| `.../image` | Image archive storage and safe tar/tar.gz rootfs extraction. |
| `.../runtime` | Java gRPC gateway to the Go daemon. |
| `.../service/ContainerService.java` | Validation, create/stop/log operations, startup reconciliation. |
| `.../state` | Container records, JSON persistence, lifecycle state, IP allocator. |
| `java-control-plane/src/main/resources/application.properties` | Runtime socket and data directory defaults/environment overrides. |
| `scripts/verify-host.sh` | Preflight checks for privileged Linux integration runs. |

## 3. What has been implemented in the recent work

These are the concrete additions that a new maintainer may otherwise mistake for pre-existing behavior:

- Added Java JSON snapshot persistence for container views. Each snapshot is written to a same-directory temporary file, flushed, and atomically moved into place; the control plane holds a state-file lock so a second Java instance cannot write the same snapshot concurrently.
- Added daemon `ListActiveContainers` support and Java startup reconciliation. Java now restores leases, reconnects `Wait` monitoring for sessions still owned by Go, marks stale saved records no longer active, and adds records for live Go sessions absent from Java's snapshot.
- Added `log_path` to the runtime RPC contract. Detached Go processes write stdout and stderr to the per-container `container.log`; Go serves bounded log reads, Java proxies them over HTTP, and the CLI has a `logs` command.
- Added baseline child hardening: `no_new_privs`, `nosuid`/`nodev` root mount flags, read-only restrictive `/proc`, and a private minimal `/dev` plus `devpts` setup.
- Added/updated Go integration and transport checks, the host preflight script, Makefile targets, and handover-oriented READMEs.

When modifying these features, remember the ownership boundary: Java owns the durable registry and image files; Go owns live process sessions and kernel setup. The protobuf file is the cross-language API. A feature is only fully integrated after both clients/servers and generated bindings agree.

## 4. Request and process lifecycle

### Image staging

`bin/minidock build -t NAME ARCHIVE` stages an existing `.tar` or `.tar.gz` rootfs archive in Java's image directory. This is the intentionally simple image workflow for MiniDock. Dockerfile builds, remote registries, and Docker-compatible image formats are not needed for the current project purpose.

### Container creation

1. CLI sends a create request to Java (`POST /api/v1/containers`).
2. Java validates image name, command, environment, memory/CPU limits, attach/TTY options, and network setting.
3. Java allocates a short container ID and, when networking is enabled, an IP address. It persists a `PREPARING` record.
4. Java creates a per-container directory, extracts the image to `rootfs/`, and calls Go `Start` with command, environment, resource limits, network details, attach options, and log path.
5. Go validates the request, creates the container session, launches/re-execs its runtime child in namespaces, applies cgroups and optional networking, and waits for the child setup acknowledgement.
6. The child prepares mounts, enters the rootfs with `pivot_root`, sets `no_new_privs`, and execs the requested command.
7. Go returns the host PID and selected IP. Java transitions its record to `RUNNING` and begins a `Wait` RPC in the background.
8. On process exit, Java records the exit status, releases the IP, and removes the extracted `rootfs`. It retains the container directory and detached `container.log` for inspection.

Attached runs use a PTY when requested and stream terminal input/output and resize events through gRPC. Detached runs direct stdout/stderr to `container.log`.

### Stop and logs

- `POST /api/v1/containers/{id}/stop` asks Go to stop the session; Java lifecycle state is updated through the completion flow.
- `GET /api/v1/containers/{id}/logs?offset=N&limit=N` returns a byte range from the detached log. The maximum requested chunk is 1 MiB. Response headers include `X-MiniDock-Log-Size` and `X-MiniDock-Log-Offset`.
- CLI equivalents are `bin/minidock stop ID` and `bin/minidock logs ID [OFFSET] [LIMIT]`.

### Startup recovery

Java persists container snapshots to a local JSON file using a temporary file and atomic rename. On startup, `ContainerService` calls Go `ListActiveContainers`, compares daemon sessions with saved Java records, restores active IP leases, reattaches wait monitoring to matching containers, marks saved-but-missing sessions exited/failed, cleans stale rootfs trees, and records daemon sessions missing from the Java state file as recovered entries.

This is **best-effort process recovery**, not database-backed transactional state: Java state is an atomic snapshot file, while Go's active session map exists only for the lifetime of the daemon. If Go restarts, its sessions disappear and Java reconciliation will mark them as no longer active.

## 5. Commands to build, run, and operate

Build both layers:

```sh
make build
```

Run unit/non-privileged checks:

```sh
make test
```

Run the Go daemon and Java API in separate terminals:

```sh
make run-go
```

```sh
make run-java
```

The simple user flow is:

```sh
bin/minidock build -t alpine-base alpine-minirootfs.tar.gz
bin/minidock images
bin/minidock run -it -m 256M -c 50 alpine-base /bin/sh
bin/minidock ps
bin/minidock stop CONTAINER_ID
bin/minidock logs CONTAINER_ID
```

`-it` attaches a terminal. For background execution use `-d`; `--no-network` disables bridge setup. Check `bin/minidock` usage for exact supported flags.

The privileged integration target is:

```sh
make test-integration
```

It invokes `sudo scripts/verify-host.sh` followed by Go tests. Run it from an interactive Linux host terminal where sudo can authenticate. The preflight checks cgroup v2 CPU/memory controllers, required network tools, and IPv4 forwarding access. The current end-to-end test attempts real namespace and mount setup when permitted, but it uses a temporary fake cgroup filesystem and a mocked network command runner. It does **not** verify writing limits to the host's delegated cgroup hierarchy or creating a real bridge/veth and firewall rules.

## 6. Configuration and storage

| Setting | Default | Used by |
| --- | --- | --- |
| `MINIDOCK_SOCKET` | `/tmp/minidock-runtime.sock` | Java client and Go daemon; must match. |
| `MINIDOCK_API_ADDRESS` | `127.0.0.1` | Java HTTP bind address. |
| `MINIDOCK_IMAGES_DIR` | `${user.home}/.local/share/minidock/images` | Java image archives. |
| `MINIDOCK_CONTAINERS_DIR` | `${user.home}/.local/share/minidock/containers` | Java rootfs/log storage and Go detached logs. Set identically if Java and Go have different home directories. |
| `MINIDOCK_STATE_FILE` | `${user.home}/.local/share/minidock/state.json` | Java lifecycle snapshots; the sibling `.lock` file enforces one control-plane writer. |
| `MINIDOCK_CGROUP_ROOT` | Runtime default under `/sys/fs/cgroup` | Go cgroup subtree; host must delegate CPU/memory controllers and permit writes. |

The socket is created with mode `0660`; filesystem ownership/group permissions must allow the Java process to connect. Java's state and container metadata are local files. Images are local rootfs archives, not OCI image references.

## 7. Linux behavior and security boundaries

The Go child uses user, PID, mount, network, UTS, and IPC namespaces. The re-exec pattern is required because PID namespaces take effect at process creation. Go sets a parent-death signal for its namespace init process and enables `no_new_privs` before executing the workload.

The root filesystem is bind-mounted with `nosuid` and `nodev`, then installed as the process root with `pivot_root`; the old root is detached. `/proc` is mounted read-only with `nosuid`, `noexec`, and `nodev`. `/dev` is a private tmpfs with selected device files and private `devpts`. These are useful baseline restrictions, but the rootfs remains writable and the runtime does not yet implement a complete capability drop, seccomp policy, LSM profile, user-ID mapping policy, or comprehensive syscall/filesystem attack-surface reduction.

CPU and memory limits rely on cgroups v2. Network mode relies on Linux bridge/veth support and host network administration privileges. The network package installs forwarding/masquerade rules for its configured subnet and attempts to undo its owned rules and restore forwarding state on normal shutdown. A forced host/daemon crash can bypass normal cleanup; inspect host firewall and forwarding state after interrupted experiments.

Do development on a disposable VM or otherwise controlled Linux machine. Avoid testing as root against arbitrary paths. Use a dedicated rootfs archive and the configured MiniDock data/cgroup directories.

## 8. Verification status and issues that affect functionality

The latest implementation pass completed these checks:

- `go test -count=1 ./...` and `go vet ./...` successfully in the development environment.
- Maven package build successfully (`./mvnw -q -Dmaven.repo.local=/tmp/minidock-maven-cache -DskipTests package`). This compiles Java but skips Java tests.
- Shell syntax checks, `git diff --check`, and `make -n test-integration` successfully.

The privileged `make test-integration` run was attempted but could not start because sudo could not obtain a password/non-interactive terminal in the development environment. Therefore real host namespace/mount/devpts/cgroup/network execution remains unverified on the target Zorin/Linux host. Rootless Go tests skip the kernel-dependent end-to-end test when the environment returns permission errors; a skip is not proof that host isolation works.

### Must resolve or verify before relying on end-to-end operation

| Priority | Issue | Effect if unresolved | Next maintainer action |
| --- | --- | --- | --- |
| **P0: namespace/rootfs host verification** | The full kernel end-to-end test has not passed on the target Linux host. Namespace creation, `pivot_root`, and `/proc`/`/dev` mounts depend on host kernel policy and permissions. | `run` can fail before starting the requested process. Unit tests and skipped integration tests cannot establish this works on Zorin. | Run `make test-integration` from an interactive terminal with sudo on a disposable Linux VM/host. Fix each actual failure and rerun until the namespace/rootfs portion passes; record any unsupported host restriction explicitly. |
| **P0: real cgroup/network verification** | The current system test fakes cgroup files and mocks network commands. The host script checks prerequisites but does not attempt to create a cgroup, bridge, veth, or firewall rule. | CPU/memory limits or container networking may fail in actual use even if `make test` passes. | Add a separate opt-in privileged test on a disposable host that creates a temporary delegated cgroup and a real bridge/veth, verifies controls/routes, and reliably removes every resource. Then run it on Zorin. |
| **P0: shared runtime settings** | Java and Go must use the same socket path; detached logs require both to resolve the same containers directory. Socket mode is `0660`, so filesystem ownership/group access also matters. | Java cannot call Go, or log retrieval reports missing files even though Go wrote them. | Start both as compatible users, set matching `MINIDOCK_SOCKET`, and set the same `MINIDOCK_CONTAINERS_DIR` whenever home directories differ. Verify the socket and directory permissions. |
| **P0: host prerequisites** | Real kernel operation requires Linux namespace/mount support; requested limits require writable cgroup v2 CPU/memory controls; networking requires `ip`, `nsenter`, `iptables`, bridge/veth support, forwarding access, and network administration capability. The preflight only checks some prerequisites. | Depending on requested options, startup fails; CPU/memory limits or outbound networking cannot be provided. | Run `scripts/verify-host.sh`, then check the exact cgroup delegation and capabilities from the daemon's execution context. Do not infer that `sudo` alone bypasses container/AppArmor/host policy. |
| **P1: failed-start and shutdown recovery** | Partial host setup and forced termination can interrupt normal cleanup; network forwarding/firewall state is host-global. | Stale bridge/veth/firewall state or temporary directories can remain and interfere with later runs. | Exercise failure injection at every setup step and forced shutdown on a disposable host; verify cleanup is idempotent and document safe recovery commands. |

### Remaining work within MiniDock's intended scope

- A local rootfs tarball workflow is the current image scope; Dockerfile builds, remote registries, OCI compatibility, image signatures, and other Docker product features are **out of scope**, not outstanding defects.
- Java persistence is a JSON snapshot with a lifetime file lock to enforce one control-plane writer. It is not a multi-process database and has no transaction/query layer.
- Detached logs are retrievable in chunks but have no rotation or size quota, so long-running noisy containers can consume disk space.
- The system needs failure-injection and concurrent lifecycle tests: Java restart while Go stays up, Go restart during start, stop/start races, corrupt state file, extraction failures, log growth, and partial bridge setup.
- Network forwarding/firewall setup is host-global and needs careful review and cleanup testing.
- The API is designed for local use and binds to loopback by default. Keep it local for MiniDock's current workflow; remote multi-user access would require authentication and authorization.
- Go's active session map is in memory. A daemon failure stops its namespace children through the parent-death signal; Java can reconcile stale records after restart, but container survival across a Go daemon failure is not part of the current MiniDock workflow.
- The rootfs remains writable, and the namespace setup is basic isolation for learning and local workloads. Running hostile workloads is outside this project's intended use; production-grade security hardening is not a current requirement.

Do not treat missing Docker features as work to implement. MiniDock's success criteria are a local workflow that stages a rootfs archive, starts a command with basic isolation and requested limits, lists and stops it, retrieves detached logs, and performs predictable cleanup and state recovery on the supported Linux host.

## 9. Good first steps for a new maintainer

1. Read this file, root `README.md`, then `protocol/runtime.proto` to understand the contract.
2. Run `make test` and `make build` before changing code.
3. Trace one `run` request through `bin/minidock` → `ContainerController` → `ContainerService` → `GoRuntimeGateway` → `transport` → `container`/`isolation`.
4. Run privileged integration checks on a disposable Linux VM and inspect failures before changing host-global firewall/cgroup behavior.
5. When changing RPC messages, update `protocol/runtime.proto` and regenerate both Go and Java bindings using the Maven/protobuf generation setup; do not manually edit generated files.
6. Keep lifecycle and cleanup behavior explicit: every partially completed start step must have a matching rollback, and tests should cover both normal exit and failed setup.

