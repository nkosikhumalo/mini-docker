# Mini Docker

A learning project split into a Go container runtime and a Java control plane.
Only the two startup entry points contain executable code. Other files are
project metadata or comments describing where implementation belongs.

## Project layout

```text
mini-docker/
├── go-runtime/                 # Linux container runtime (Go)
│   ├── cmd/minidockerd/         # Runnable gRPC daemon entry point
│   └── internal/
│       ├── cgroups/             # Cgroups v2 resource limits
│       ├── container/           # Container lifecycle and config
│       ├── isolation/           # PID, mount, network, UTS, IPC namespaces
│       ├── network/             # veth and bridge setup
│       ├── rootfs/              # Root filesystem and pivot_root
│       └── transport/           # Local control-plane communication
├── java-control-plane/          # Spring Boot REST API and lifecycle manager
│   └── src/main/java/           # Application packages
├── protocol/                    # Shared gRPC contract, when introduced
└── README.md
```

## Responsibilities

- **Go runtime:** create and stop containers; configure Linux namespaces,
  cgroups v2, root filesystems, and virtual networking.
- **Java control plane:** expose the developer API, track container state, and
  call the Go runtime.
- **Communication:** initial gRPC connection over loopback TCP (`127.0.0.1:50051`).
  The `protocol/` directory is reserved for the shared contract and generated
  bindings when lifecycle operations are designed.

## Development status

This is a runnable scaffold. The Go daemon opens a gRPC server and the Java
application starts Spring Boot; no container operations are implemented yet.

## Initial setup

- Go module metadata is in `go-runtime/go.mod`.
- Java Maven metadata is in `java-control-plane/pom.xml`.
- Linux-only runtime behavior should be kept behind the Go runtime boundary.

## Start the services

From `go-runtime/`, run `go run ./cmd/minidockerd` (requires Go 1.22+).
From `java-control-plane/`, run `./mvnw spring-boot:run` (requires Java 17+;
the Maven wrapper downloads Maven on first use). The Go process listens on
`127.0.0.1:50051`; the Java app uses Spring Boot's default HTTP port.
The gRPC listener is an empty server scaffold until the protocol and operations
are added; the Java app does not call it yet.
