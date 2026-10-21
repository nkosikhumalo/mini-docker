# Java control plane

The Spring Boot API provides container create/list/get/stop operations under
`/api/v1/containers` and image staging/list operations under `/api/v1/images`.
The `bin/minidock` script provides a local CLI for these endpoints.

Image staging accepts existing `.tar` and `.tar.gz` rootfs archives. On run,
Java validates limits and command arguments, safely extracts the archive into
the container directory, allocates the requested IP, and calls Go's protobuf
gRPC `Start`. The returned host PID is included in the container view. A
background `Wait` call updates the in-memory registry with exit status; stop
calls Go's `Stop` RPC. Registry contents are not persisted across Java
restarts.

Storage paths default to `${user.home}/.local/share/minidock/images` and
`${user.home}/.local/share/minidock/containers`. Configure the runtime socket
with `MINIDOCK_SOCKET` to match the Go daemon.
