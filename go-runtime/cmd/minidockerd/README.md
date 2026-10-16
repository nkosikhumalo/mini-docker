# Daemon entry point

The Go executable entry point will live in this directory. It will load runtime
configuration, initialize the transport server, and coordinate shutdown.
Keep container and Linux-specific implementation details in `internal/`.
