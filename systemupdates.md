# MiniDock System Handover

This document is an orientation guide for the next person working on MiniDock. It explains what the project currently does, how its parts communicate, how to run it, where important behavior lives, and which gaps still need attention. Treat the source code and `protocol/runtime.proto` as authoritative if this document and implementation ever disagree.

## 1. What MiniDock is

MiniDock is a deliberately small Linux learning project: it stages a rootfs tarball and runs a command from it with basic process, filesystem, resource, and optional network isolation. It is not intended to reproduce Docker's full product surface or internal architecture. It has two main programs:

- **Java control plane** (`java-control-plane`): accepts HTTP requests and CLI calls, validates container requests, stores/extracts images, assigns container IDs and IPs, persists lifecycle metadata, and asks Go to run/stop containers.
- **Go runtime daemon** (`go-runtime`, executable `minidockd`): receives local gRPC requests, creates Linux isolation and resource controls, launches the command, and reports its PID, exit status, and logs.

The Java API is bound to `127.0.0.1:8080`. Java and Go communicate over a Unix domain socket, default `/tmp/minidock-runtime.sock`, using protobuf gRPC. Keep new work proportional to this project's mini scope: implement what the documented local workflow needs, and avoid Docker-compatible features or complexity unless the project explicitly chooses to expand.

## 2. Repository map

| Path | Responsibility |
| --- | --- |
| `bin/minidock` | Bash CLI for image/container HTTP endpoints and `ask` prompt-to-container requests. |
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
| `scripts/minidock_ask.py` | OpenRouter request, JSON parsing, and validation of generated run specs. |
| `scripts/minidock_ask_build.py` | OpenRouter request and validation of a project summary and recommendation ranking against supported local build recipes. |
| `scripts/minidock_build.py` | Builds Vite, Go, and Maven Java source directories into Linux rootfs archives. |
| `go-runtime/cmd/minidock-web` | Small Go static-file server included in Vite-built images. |
| `go-runtime/cmd/minidock-ui` | Bubble Tea and Lip Gloss command views, AI recipe selector, and interactive MiniDock dashboard. |
| `run.sh` | Builds MiniDock, starts the Go daemon and Java API, then opens the Bubble Tea dashboard in an interactive terminal. |

## 3. What has been implemented in the recent work

These are the concrete additions that a new maintainer may otherwise mistake for pre-existing behavior:

- Added Java JSON snapshot persistence for container views. Each snapshot is written to a same-directory temporary file, flushed, and atomically moved into place; the control plane holds a state-file lock so a second Java instance cannot write the same snapshot concurrently.
- Added daemon `ListActiveContainers` support and Java startup reconciliation. Java now restores leases, reconnects `Wait` monitoring for sessions still owned by Go, marks stale saved records no longer active, and adds records for live Go sessions absent from Java's snapshot.
- Added `log_path` to the runtime RPC contract. Detached Go processes write stdout and stderr to the per-container `container.log`; each log is now capped at 10 MiB with a truncation marker, Go serves bounded log reads, Java proxies them over HTTP, and the CLI has a `logs` command.
- Added baseline child hardening: `no_new_privs`, `nosuid`/`nodev` root mount flags, read-only restrictive `/proc`, and a private minimal `/dev` plus `devpts` setup.
- Added `minidock ask`: the CLI sends a prompt and staged image list to OpenRouter, validates the returned MiniDock run spec, shows it for confirmation, then submits confirmed runs through the existing Java API. `OPENROUTER_API_KEY` and optional `OPENROUTER_MODEL` load from the ignored root `.env` file; the default model is `openrouter/auto`.
- Added offline tests for OpenRouter JSON/config validation and the CLI-to-container API payload. A mocked-provider live app smoke run staged a temporary static BusyBox rootfs, confirmed a proposal, ran it through the real namespace/runtime path with networking disabled, and retrieved the expected output from logs.
- Rootfs extraction accepts the conventional tar root directory entry (`.`/`./`) while continuing to reject non-directory entries that resolve to the root. Verified by building a `tar -C rootfs .` BusyBox image and running it through the real Java/Go path.
- `bin/minidock build -t NAME PROJECT_DIR` now supports the frontend project in `frontend/`: it runs its npm build on the host, then packages only `dist/` plus a small Go static server into a Linux rootfs archive and stages it through the existing Java image API. This is a small Vite adapter, not a general Dockerfile or arbitrary project builder. It requires a supported Node.js version, npm, Go, and Java API access; npm scripts run with the caller's host-user permissions. The app is started with `/usr/bin/minidock-web` on port 8080.
- Added `bin/minidock ask-build [ -t NAME ] PROJECT_DIR "description"`. OpenRouter receives a small project profile (manifest/dependency metadata, no source files or `.env` values), describes the app, ranks only build recipes MiniDock reports as available, and can ask up to four follow-up questions before proposing the plan. The image name defaults from the detected project name. A local arrow-key menu shows fixed recipe choices; the user selects one and confirms before execution. Source adapters now support Vite, Go main packages, and Maven executable JAR projects. MiniDock maps choices to fixed local adapter code and never executes model-generated shell commands. Java images bundle a jlink runtime and required Linux shared libraries; Go builds use CGO-disabled Linux binaries. OpenRouter credentials are removed from the environment inherited by project build tools. Other project types still need a rootfs archive or a future explicit adapter.
- The image staging API now returns the resolved archive path along with its name so CLI users can find the built image in MiniDock's image store.
- On 2026-10-27, `ask-build` produced and the user approved a `vite-static` plan for `frontend-app`. The first build attempts stopped before running npm because the host's Node.js 18.19.1 does not meet the project's Vite 8 requirement (Node 20.19+ or 22.12+). Downloaded official Node.js 22.23.4 into `/tmp`, verified its SHA-256 against the official checksum file, and used it without changing system Node. Vite production build, static server compilation, packaging, and Java image staging then succeeded; the existing container was not stopped or restarted.
- Added Go and Maven Java source adapters. Go builds a CGO-disabled Linux main package into `/usr/bin/<name>`. Maven builds an executable JAR (invoking Spring Boot `repackage` when its plugin is configured), uses `jdeps` to select JDK modules, creates a minimized `jlink` runtime, and bundles its required Linux shared libraries. Maven artifacts/cache default to `/tmp/minidock-maven-cache`; override with `MINIDOCK_MAVEN_REPO`. On 2026-10-27, built and staged `mini-go-runtime` from `go-runtime/cmd/minidockd` (3.5 MiB) and `mini-java-control-plane` from `java-control-plane` (92 MiB). The Java image includes a runtime, but the control-plane app still expects the Go daemon's Unix socket for container operations. Neither image was launched during this build.
- On 2026-10-29, the local Java API at `127.0.0.1:8080` was not running, so MiniDock could not list or launch staged images. Did not start services due the user's prior instruction not to start MiniDock. Extracted the Java image to `/tmp` and confirmed its bundled JRE runs `java --version` as OpenJDK 21.0.12. The Spring Boot app and Go daemon images have not been launched through MiniDock.
- Replaced the Python/ANSI CLI renderer and curses build selector with a Go UI helper built using Bubble Tea v1.3.0 and Lip Gloss v1.0.0 (kept compatible with the module's Go 1.22 target). `images`, `ps`, `run`, `stop`, `build`, and AI proposal output now use styled views on a TTY; API results remain JSON when output is piped or `NO_COLOR` is set. The ask-build recipe selector uses Bubble Tea arrow-key input. `make build-go` builds `bin/minidock-ui`; the CLI falls back to `go run` if that binary is absent.
- Added an interactive Bubble Tea dashboard to `minidock-ui app`: it refreshes images and active containers, lets users launch containers from a form, build images through the AI-assisted `ask-build` flow, inspect container details and logs, and confirm stop actions. The AI build form asks for a project folder and the user's goal; the image name defaults from the detected project. The AI can ask up to four follow-up questions, then returns supported recipes for user selection and confirmation; local adapters perform the build. `run.sh` opens this dashboard after the Go and Java services are healthy; closing it exits `run.sh` and its cleanup stops both services. `bin/minidock ui` opens another dashboard session against a running API.
- Added root `run.sh` to build MiniDock, start the Go daemon and Java API, and keep them running until Ctrl+C. It does not build an image or start a container. The user runs `bin/minidock build -t NAME frontend/` in another terminal to build and stage an image; the static web server listens on container port 8080 when run later. `MINIDOCK_API_PORT` changes the control-plane port. The frontend's own API URL comes from Vite configuration; MiniDock's Java control plane is not the frontend application's backend.
- Previously staged `frontend-app.tar.gz` through the real Go daemon and Java API as a smoke check; the image is about 2.2 MiB. The generated static server passed homepage, SPA route, JavaScript asset, and missing-asset checks outside a container. `run.sh` itself now leaves image building and container creation to separate user commands.
- On 2026-10-27, rebuilt/staged `frontend-app` with `bin/minidock build -t frontend-app frontend/`; the archive contains `usr/bin/minidock-web` and the Vite `dist/` assets. The first real start failed because the daemon could not re-execute its binary through `/home/nkosi` (mode `0750`). Re-executing through inherited `/proc/self/fd/5` fixed that failure and allowed the child to enter setup.
- The next child log showed `set container hostname: operation not permitted`. Namespace UID/GID mappings now accept `MINIDOCK_HOST_UID`/`MINIDOCK_HOST_GID`; `run.sh` supplies the invoking user's IDs, and root daemons can fall back to `SUDO_UID`/`SUDO_GID`. `SysProcAttr.Credential` sets namespace UID/GID 0 after mapping while skipping `setgroups`. Java preserves failed-start `container.log` and returns HTTP 404 for a missing Go log instead of 500. After restarting with this change, the frontend container started successfully.
- Live frontend container `29a32ccd2e2a` ran `/usr/bin/minidock-web` at `10.0.0.2:8080`. Verified from the host: homepage HTTP 200, referenced JavaScript asset HTTP 200 (340,516 bytes), SPA route `/login` HTTP 200 with the app shell, nonexistent asset HTTP 404, and startup log reported the expected root and listen address. The container remains running. This verifies MiniDock's Vite image build, rootfs extraction, namespace/mount setup, bridge reachability, and static serving on this host; the frontend's external API behavior was not part of this HTTP smoke check.
- Added/updated Go integration and transport checks, the host preflight script, Makefile targets, and handover-oriented READMEs.

When modifying these features, remember the ownership boundary: Java owns the durable registry and image files; Go owns live process sessions and kernel setup. The protobuf file is the cross-language API. A feature is only fully integrated after both clients/servers and generated bindings agree.

## 4. Request and process lifecycle

### Image staging

`bin/minidock build -t NAME ARCHIVE` stages an existing `.tar` or `.tar.gz` rootfs archive in Java's image directory. `bin/minidock build -t NAME PROJECT_DIR` also supports Vite frontend source directories by running the host npm build and packaging static output with MiniDock's web server. Other project types still need a prepared Linux rootfs archive. This is the intentionally simple image workflow for MiniDock. Dockerfile builds, remote registries, and Docker-compatible image formats are not needed for the current project purpose.

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

It invokes `sudo scripts/verify-host.sh` followed by the Go checks. These checks include a kernel-dependent namespace/rootfs path, but the existing system test still uses a fake cgroup filesystem and mocked network commands.

For actual host cgroup, bridge/veth, and firewall setup, use the separate opt-in target:

```sh
MINIDOCK_CGROUP_ROOT=/sys/fs/cgroup/minidock-e2e make e2e
```

Run it on a disposable Linux host with sudo available and `MINIDOCK_CGROUP_ROOT` pointing to an existing dedicated delegated cgroups v2 subtree with CPU and memory controllers enabled. The script checks host prerequisites and refuses to run if `minidock0` already exists or `10.0.0.0/24` is already routed. The integration test creates and cleans its own bridge, veth, firewall rules, cgroup, and temporary rootfs on normal test completion. The target passed on the target host on 2026-10-22. Do not use this target on a machine where those host-global changes are unacceptable.

## 6. Configuration and storage

| Setting | Default | Used by |
| --- | --- | --- |
| `MINIDOCK_SOCKET` | `/tmp/minidock-runtime.sock` | Java client and Go daemon; must match. |
| `MINIDOCK_HOST_UID` / `MINIDOCK_HOST_GID` | Daemon UID/GID; root daemon falls back to `SUDO_UID` / `SUDO_GID` | Host IDs mapped to container namespace root. `run.sh` sets these to the invoking user's IDs so namespace children can access user-owned rootfs and data paths. |
| `MINIDOCK_IMAGES_DIR` | `${user.home}/.local/share/minidock/images` | Java image archives. |
| `MINIDOCK_CONTAINERS_DIR` | `${user.home}/.local/share/minidock/containers` | Java rootfs/log storage and Go detached logs. Set identically if Java and Go have different home directories. |
| `MINIDOCK_STATE_FILE` | `${user.home}/.local/share/minidock/state.json` | Java lifecycle snapshots; the sibling `.lock` file enforces one control-plane writer. |
| `MINIDOCK_CGROUP_ROOT` | Runtime default under `/sys/fs/cgroup` | Go cgroup subtree; host must delegate CPU/memory controllers and permit writes. |

The socket is created with mode `0660`; filesystem ownership/group permissions must allow the Java process to connect. Java's state and container metadata are local files. Images are local rootfs archives, not OCI image references.

## 7. Linux behavior and security boundaries

The Go child uses user, PID, mount, network, UTS, and IPC namespaces. The re-exec pattern is required because PID namespaces take effect at process creation. Go sets a parent-death signal for its namespace init process and enables `no_new_privs` before executing the workload.

The root filesystem is bind-mounted with `nosuid` and `nodev`, then installed as the process root with `pivot_root`; the old root is detached. `/proc` is mounted read-only with `nosuid`, `noexec`, and `nodev`. `/dev` is a private tmpfs with selected device files and private `devpts`. These are useful baseline restrictions, but the rootfs remains writable and the runtime does not yet implement a complete capability drop, seccomp policy, LSM profile, user-ID mapping policy, or comprehensive syscall/filesystem attack-surface reduction.

CPU and memory limits rely on cgroups v2. Network mode relies on Linux bridge/veth support and host network administration privileges. The network package installs forwarding/masquerade rules for its configured subnet and attempts to undo its owned rules and restore forwarding state on normal shutdown. At daemon startup, MiniDock removes empty per-container cgroups from its configured subtree; populated cgroups are preserved. A forced host/daemon crash can still leave host veths, firewall rules, forwarding state, and rootfs temporary directories. MiniDock does not automatically sweep these host network resources because interface names alone do not prove ownership; inspect host state after interrupted experiments.

Do development on a disposable VM or otherwise controlled Linux machine. Avoid testing as root against arbitrary paths. Use a dedicated rootfs archive and the configured MiniDock data/cgroup directories.

## 8. Verification status and issues that affect functionality

The latest implementation pass completed these checks:

- `make test` passed the Go test suite and `go vet`; its first Maven invocation could not write to the default read-only `~/.m2`, then `./mvnw -Dmaven.repo.local=/tmp/minidock-maven-cache test` compiled Java successfully. Maven found no Java test sources.
- `make build-go` built both Go binaries.
- A combined local smoke run started `minidockd` and the Spring Boot control plane, confirmed Java connected to Go over gRPC, and received `200` from both image and container list APIs.
- `make test-ask` passed offline AI schema/config checks and CLI submission/missing-key checks. A mocked OpenRouter completion also drove a confirmed request through the real local Java API and Go daemon; the temporary BusyBox workload ran isolated, exited, and returned its expected log output.
- Shell syntax checks, `git diff --check`, and `make -n test-integration` successfully.

The real-host `make e2e` test subsequently passed on the target Zorin OS host on 2026-10-22, exercising the namespace/rootfs, cgroup, bridge/veth, and firewall setup/normal-cleanup path. This is one successful host run, not broad distro compatibility coverage; forced-termination behavior is not covered.

The AI workflow test used a mocked provider response. No live OpenRouter request was made because the local `.env` key is blank; the production provider call and the user's actual OpenRouter model/account settings remain unverified.

### Must resolve or verify before relying on end-to-end operation

| Priority | Issue | Effect if unresolved | Next maintainer action |
| --- | --- | --- | --- |
| **RESOLVED 2026-10-22: namespace/rootfs host verification** | The privileged namespace/rootfs path passed as part of `make e2e` on the target host. Namespace creation, `pivot_root`, and `/proc`/`/dev` mounts remain dependent on host kernel and security policy. | One host configuration is verified; other distro and policy combinations may still reject operations. | Repeat `make e2e` on each additional supported host configuration before claiming compatibility. |
| **RESOLVED 2026-10-22: real cgroup/network verification** | The dedicated `make e2e` test passed on the target host, exercising real cgroup writes and bridge/veth/firewall setup. The existing general system test still uses fake cgroup files and mocked network commands. | The privileged path has passed once on the target host; repeat after changes to host integration. | Re-run `MINIDOCK_CGROUP_ROOT=... make e2e` on a controlled Linux host after changes to cgroups or network setup. |
| **P0: shared runtime settings** | Java and Go must use the same socket path; detached logs require both to resolve the same containers directory. Socket mode is `0660`, so filesystem ownership/group access also matters. | Java cannot call Go, or log retrieval reports missing files even though Go wrote them. | Start both as compatible users, set matching `MINIDOCK_SOCKET`, and set the same `MINIDOCK_CONTAINERS_DIR` whenever home directories differ. Verify the socket and directory permissions. |
| **P1: host prerequisites and distro coverage** | Daemon startup now reports root status, visible cgroups v2 CPU/memory controllers, and whether `ip`, `nsenter`, and `iptables` are present. These are diagnostics because namespace-only operation can work without root, and cgroups/network are feature-specific. The real host path has only been exercised on the project's Zorin OS development host; its release/kernel version was not recorded. | Missing host support is reported early, but kernel/security-policy denials and delegated cgroup write access can still fail when a feature is requested. Other distributions remain unverified. | Use the actionable startup diagnostics, verify the delegated subtree for limits, and run `make e2e` before claiming support for another distro. Check system/audit logs for AppArmor or SELinux denials; do not globally disable host security policy as a workaround. |
| **P1: forced-termination recovery** | Daemon startup now removes empty direct-child cgroups from its configured subtree and preserves populated groups. Host veth, firewall, forwarding, and temporary rootfs leftovers are not auto-swept because current state does not provide enough durable ownership information to distinguish stale MiniDock resources from unrelated host resources. | Those host resources can remain after SIGKILL or host crash and may interfere with later runs. | Add durable ownership metadata for MiniDock-created network resources, then build a conservative stale-resource recovery path; exercise forced termination and recovery on a disposable host. |

### Remaining work within MiniDock's intended scope

- A local rootfs tarball workflow is the current image scope; Dockerfile builds, remote registries, OCI compatibility, image signatures, and other Docker product features are **out of scope**, not outstanding defects.
- Java persistence is a JSON snapshot with a lifetime file lock to enforce one control-plane writer. It is not a multi-process database and has no transaction/query layer.
- Detached logs are retained per container and retrievable in chunks. Each log is capped at 10 MiB; once full, further output is discarded and a truncation marker is written.
- The system needs failure-injection and concurrent lifecycle tests: Java restart while Go stays up, Go restart during start, stop/start races, corrupt state file, extraction failures, log growth, and partial bridge setup.
- Network forwarding/firewall setup is host-global and needs careful review and cleanup testing.
- The API is designed for local use and is forced to bind to loopback. It has no authentication or authorization; remote multi-user access would require adding access control and deliberately changing the binding guard.
- Source build support currently covers Vite static frontends only; it runs the npm build script on the host and does not support arbitrary pre-build commands or Dockerfiles. General host-port publishing is not part of the container API; direct access through a running container's bridge IP depends on host firewall policy.
- Go's active session map is in memory. A daemon failure stops its namespace children through the parent-death signal; Java can reconcile stale records after restart, but container survival across a Go daemon failure is not part of the current MiniDock workflow.
- The rootfs remains writable, and the namespace setup is basic isolation for learning and local workloads. Running hostile workloads is outside this project's intended use; production-grade security hardening is not a current requirement.

### Source scan findings (2026-10-22)

The following findings came from a source review of the Java API/control plane, Go runtime, CLI, image extraction, and network setup. They are implementation or operational risks within MiniDock's current scope; they do not imply that Docker product features are missing.

| Priority | Finding | Effect | Suggested action within MiniDock's scope |
| --- | --- | --- | --- |
| **RESOLVED 2026-10-22: firewall rule ownership** | In `go-runtime/internal/network/bridge_linux.go`, the manager now records only rules it actually appended; an identical pre-existing rule is used but not claimed for deletion. | Normal shutdown no longer deletes a matching rule that predates this manager. | Keep ownership tracking intact and cover it with a fake-runner cleanup check when tests are next updated. |
| **RESOLVED 2026-10-22: start deadline and cancellation** | Go's `runtimeServer.Start` now cancels and waits for setup rollback when the RPC context expires before startup acknowledgement. Java sends `Stop` and waits for `Wait` after a failed start before removing the extracted rootfs. If cleanup cannot be confirmed, Java retains the rootfs rather than deleting it under a possibly active setup. | Startup should no longer race rootfs deletion when the start call times out. This change has not yet been exercised with a slow-start or disconnect integration case. | Add a lifecycle regression case for start timeout/client disconnect when test work resumes; verify the cleanup wait and retained-rootfs fallback. |
| **RESOLVED 2026-10-22: unauthenticated API exposure** | `LoopbackOnlyWebServerCustomizer` now forces the HTTP server to bind to `127.0.0.1`, including when a generic Spring `server.address` override is supplied. | The API remains unauthenticated, but the supported server configuration no longer exposes it on a network interface. | Keep the API loopback-only. If remote access becomes an explicit requirement, add access control before changing this binding behavior. |
| **RESOLVED 2026-10-22: unbounded detached logs** | Detached stdout/stderr now passes through a synchronized writer capped at 10 MiB per container log. The writer records a truncation marker and discards additional output without breaking the workload's output pipe. | A noisy workload can no longer grow its detached log without bound. | Keep the cap documented and revisit it only if MiniDock's local workflow needs a different limit. |
| **RESOLVED 2026-10-22: container list API parameter binding** | `ContainerController` now names its `all`, `offset`, and `limit` query parameters and `id` path parameters explicitly. A combined app smoke test found that `/api/v1/containers` returned HTTP 400 because Maven does not retain Java parameter names for Spring reflection by default. | Container list and parameterized container endpoints no longer depend on compiler `-parameters` metadata. | Keep web request parameter names explicit and add Java API tests when a Java test suite is introduced. |
| **PARTIALLY RESOLVED 2026-10-22: forced-termination cleanup** | Daemon startup removes empty direct-child cgroups from the configured cgroup subtree and preserves populated groups. Durable ownership records for network interfaces, firewall rules, forwarding state, and temporary rootfs paths are not implemented, so those are not swept automatically. | Stale cgroups are recovered on restart; other host-global or temporary leftovers may remain after SIGKILL or host crash. | Add durable ownership metadata before attempting an automatic network or filesystem sweep; test forced termination and recovery on a disposable host. |
| **P2: archive resource limits** | Multipart upload accepts up to 8 GB and rootfs extraction caps regular-file contents at 8 GiB, but extraction does not cap archive entry count or metadata-only entries. | Large uploads and archives with many directories/links can still consume disk, memory, and request-processing time. The risk is greater if the API is reachable by untrusted clients. | Keep upload limits intentionally modest for the learning workflow and add an entry-count bound; consider request/concurrency limits if local resource exhaustion becomes a problem. |
| **P2: bridge name/subnet collision** | The network manager reuses an existing `minidock0`, replaces its address with `10.0.0.1/24`, and brings it up, even when it did not create that interface. | An unrelated interface using that reserved name or subnet can have its host networking changed. | Document `minidock0` and `10.0.0.0/24` as reserved for MiniDock and fail with a clear diagnostic when an existing bridge has unexpected configuration. Keep this behavior isolated to opted-in networking. |

These findings are based on static source inspection plus the privileged `make e2e` run reported on 2026-10-22. That run confirms the exercised cgroup, namespace/rootfs, bridge/veth, and firewall setup/normal-cleanup path on the target host; it does not verify forced-termination recovery.

Do not treat missing Docker features as work to implement. MiniDock's success criteria are a local workflow that stages a rootfs archive, starts a command with basic isolation and requested limits, lists and stops it, retrieves detached logs, and performs predictable cleanup and state recovery on the supported Linux host.

## 9. Good first steps for a new maintainer

1. Read this file, root `README.md`, then `protocol/runtime.proto` to understand the contract.
2. Run `make test` and `make build` before changing code.
3. Trace one `run` request through `bin/minidock` → `ContainerController` → `ContainerService` → `GoRuntimeGateway` → `transport` → `container`/`isolation`.
4. Run privileged integration checks on a disposable Linux VM and inspect failures before changing host-global firewall/cgroup behavior.
5. When changing RPC messages, update `protocol/runtime.proto` and regenerate both Go and Java bindings using the Maven/protobuf generation setup; do not manually edit generated files.
6. Keep lifecycle and cleanup behavior explicit: every partially completed start step must have a matching rollback, and tests should cover both normal exit and failed setup.

