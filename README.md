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
bin/minidock run -m 256M -c 50 alpine-base /bin/sh -c 'echo hello'
bin/minidock ps
bin/minidock stop CONTAINER_ID
```

`build` stages an existing `.tar` or `.tar.gz` rootfs archive; it does not
build a Dockerfile. `run` returns after Go confirms startup and reports the
container ID and host PID. It currently runs detached and does not forward the
caller's terminal input/output. Java exposes the same operations through
`/api/v1/images` and `/api/v1/containers`.

## Storage and host requirements

Image archives and extracted containers default to
`${user.home}/.local/share/minidock/{images,containers}`. Set
`MINIDOCK_IMAGES_DIR` or `MINIDOCK_CONTAINERS_DIR` to change those paths. Java
keeps lifecycle records in memory; they reset when Java restarts.

The Go runtime requires Linux. Namespace creation and mounts depend on host
kernel policy. CPU and memory limits require a writable delegated cgroups v2
hierarchy; set `MINIDOCK_CGROUP_ROOT` to its MiniDock subdirectory. Container
networking requires `iproute2`, `nsenter`, and permission to create bridge and
veth devices. MiniDock configures the bridge and default route, while host
forwarding/NAT for external network access must be configured separately.

The Java-to-Go contract is protobuf gRPC over the local Unix socket. Go's
`Start`, `Wait`, and `Stop` operations let Java acknowledge startup with a PID,
wait for exit status, and stop the workload.
