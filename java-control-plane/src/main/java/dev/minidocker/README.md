# Java control plane

The Spring Boot API provides container create/list/get/stop operations under
`/api/v1/containers` and image staging/list operations under `/api/v1/images`.
The `bin/minidock` script provides a local CLI for these endpoints.

Image staging accepts existing `.tar` and `.tar.gz` rootfs archives. On run,
Java validates limits and command arguments, safely extracts the archive into
the container directory, allocates the requested IP, and calls Go's protobuf
gRPC `Start`. The returned host PID is included in the container view. A
background `Wait` call updates the in-memory registry with exit status; stop
calls Go's `Stop` RPC. Container metadata is flushed to a temporary file and
atomically replaced in `state.json` by default; a sibling lock file enforces
one Java writer. Override the state path with `MINIDOCK_STATE_FILE`. On startup Java asks Go for
live sessions, restores matching records and IP leases, and marks stale
preparing/running records failed or exited.

Storage paths default to `${user.home}/.local/share/minidock/images` and
`${user.home}/.local/share/minidock/containers`. Detached container output is
retained as `container.log` and is available at `GET /api/v1/containers/{id}/logs`.
Configure the runtime socket with `MINIDOCK_SOCKET` to match the Go daemon. If
Java and Go run with different home directories, configure the same
`MINIDOCK_CONTAINERS_DIR` in both processes.
