# Runtime protocol

The Go jailer exposes gRPC over a Unix domain socket with gRPC's JSON content
subtype. `minidock.runtime.v1.RuntimeService/Run` accepts a prepared rootfs
path, command, environment, hostname, and optional CPU and memory limits. The
call blocks until the workload exits; canceling the RPC stops it. The request
and response types are in `go-runtime/internal/transport/runtime_linux.go`.
`runtime.proto` is reserved for a future protobuf wire contract and is not used
by the current JSON transport. The Java gRPC client remains to be connected.
