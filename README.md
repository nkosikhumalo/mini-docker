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
bin/minidock build -t foxvue frontend/
bin/minidock images
bin/minidock run -it -m 256M -c 50 alpine-base /bin/sh
bin/minidock run -d foxvue /usr/bin/minidock-web --root /usr/share/minidock/www --listen 0.0.0.0:8080
bin/minidock run -d --no-network alpine-base /bin/sh -c 'echo hello'
bin/minidock ps
bin/minidock stop CONTAINER_ID
bin/minidock logs CONTAINER_ID
```

`build` accepts an existing `.tar` or `.tar.gz` Linux root-filesystem archive,
or a supported Vite project directory. For Vite projects it runs the project's
`npm run build` on the host and packages the generated `dist/` assets with
MiniDock's small static web server. Node.js 20.19+ or 22.12+ and Go must be on
`PATH`; when `node_modules/` is absent, it installs dependencies with npm.
Project build scripts execute on the host with your user permissions. Other
project types still need to be assembled as a rootfs archive.

Start the MiniDock services with the root `run.sh` script:

```sh
./run.sh
```

It builds MiniDock, starts the Go daemon and Java API, and keeps them running
until you press Ctrl+C. In another terminal, build and stage the frontend image
when you're ready:

```sh
bin/minidock build -t frontend-app frontend/
```

That command builds the image but does not start a container. Its static web
server listens on container port 8080 when you later choose to run it. Change
the Java API port with `MINIDOCK_API_PORT` when starting `run.sh`. The
frontend's own backend URL comes from its Vite configuration
(`VITE_API_BASE_URL` or `frontend/.env`); MiniDock's Java API is only the
image/container control plane.

To stage any prepared rootfs archive, for example:

```sh
tar -C rootfs -czf frontend-rootfs.tar.gz .
bin/minidock build -t frontend frontend-rootfs.tar.gz
```

The archive may include the conventional `.` root-directory entry. Start the
Vite image with the `minidock-web` command shown above. Its server listens on
port 8080 inside the container. With bridge networking configured, `run`
returns a container IP; try `http://CONTAINER_IP:8080` from the host. Host
firewall policy can still block direct bridge access.

For other images, `run` uses the startup executable supplied in the command
and returns after Go confirms startup and reports the container ID and host
PID. Attached runs connect the caller's terminal to a
container PTY, including terminal resize and exit-code forwarding. Java exposes the same operations through
`/api/v1/images` and `/api/v1/containers`.

For a web frontend, the rootfs also needs a web server and the app must listen
on a container interface. MiniDock's `run` command still has no `-p` host-port
publishing option. A static bundle alone is not a running web app.

`minidock ask "run a small shell with 64 MB of memory"` asks OpenRouter to
translate a request into a MiniDock run configuration. It only offers images
already staged locally, validates the returned fields, displays the plan, and
requires confirmation before sending it through the same Java API as `run`.
The AI request does not pull images or run arbitrary model-generated shell
text; `command` is passed as an argument array. `ask` starts confirmed
containers detached and prints the normal container response.
The request text is sent to OpenRouter; do not include secrets or private
environment values in an `ask` prompt.

The CLI loads `OPENROUTER_API_KEY` and optional `OPENROUTER_MODEL` from the
root-level `.env` file without overriding shell-exported values. The file is
ignored by Git and has restrictive local permissions. Paste your key after
`OPENROUTER_API_KEY=`. The default model is OpenRouter Auto (`openrouter/auto`);
you can select another model in `.env`. Auto routes to a model at that model's
price, so check your OpenRouter account's model and spending settings.

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
