# Runtime transport

The Go service boundary used by the Java control plane belongs here. The
initial connection uses gRPC over loopback TCP on port 50051. Service methods
and generated bindings will be added when lifecycle operations are designed.
