# Container lifecycle

`container.Config` is the launch description passed through the runtime. It
validates required fields and basic cgroup/environment values before any host
resources are acquired.

`container.Run` encodes the host/child ordering: start a namespaced child and
hold it before workload execution, attach its PID to a cgroups v2 group,
configure networking, then release it to set up the root filesystem and exec
the requested command. Cleanup runs in reverse acquisition order. A backend
will connect this lifecycle to the Linux-specific isolation, cgroups, rootfs,
and network packages; those operations are not implemented yet.
