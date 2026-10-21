# Runtime protocol

`runtime.proto` is the shared protobuf contract for Java and Go. `Start`
accepts container configuration and returns the host PID and selected IP after
the runtime has completed setup. `Wait` returns the workload exit code, and
`Stop` cancels the running process. The Go messages are generated into
`go-runtime/gen/runtime/v1`; Maven generates Java messages and gRPC stubs from
this file.
