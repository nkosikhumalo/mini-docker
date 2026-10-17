# Mini Docker

A learning project split into a Go Linux jailer and a Java control plane. Java
owns the API, image preparation, and container state; Go only applies kernel
isolation and runs the prepared workload.

## Project layout

```text
mini-docker/
├── go-runtime/                 # Linux container runtime (Go)
│   ├── cmd/minidockd/           # Runnable gRPC daemon entry point
│   └── internal/
│       ├── cgroups/             # Cgroups v2 resource limits
│       ├── container/           # Container lifecycle and config
│       ├── isolation/           # PID, mount, network, UTS, IPC namespaces
│       ├── network/             # loopback setup in the network namespace
│       ├── rootfs/              # Root filesystem and pivot_root
│       └── transport/           # Local control-plane communication
├── java-control-plane/          # Spring Boot REST API and lifecycle manager
│   └── src/main/java/           # Application packages
├── protocol/                    # Shared gRPC contract, when introduced
└── README.md
```

## Responsibilities

- **Go runtime:** validate a launch request, create Linux namespaces, apply
  cgroups v2 limits, jail the process in a prepared rootfs, and run it.
- **Java control plane:** expose the developer API, track container state, and
  call the Go runtime.
- **Communication:** gRPC over `/tmp/minidock-runtime.sock` (override with
  `MINIDOCK_SOCKET`), restricted to the daemon's OS user.

## Development status

The Go runtime runs a synchronous `Run` request through namespaces,
`pivot_root`, and optional cgroups limits. Java image preparation and the Java
gRPC client remain to be implemented.

## Initial setup

- Go module metadata is in `go-runtime/go.mod`.
- Java Maven metadata is in `java-control-plane/pom.xml`.
- Linux-only runtime behavior should be kept behind the Go runtime boundary.

## Start the services

From `go-runtime/`, run `go run ./cmd/minidockd` (requires Linux and Go 1.22+).
From `java-control-plane/`, run `./mvnw spring-boot:run` (requires Java 17+;
the Maven wrapper downloads Maven on first use). The Go process listens on its
Unix socket; the Java app uses Spring Boot's default HTTP port. The Go `Run`
RPC is synchronous and the Java app does not call it yet.
