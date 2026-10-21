package dev.minidocker.state;

public enum ContainerStatus {
    PREPARING,
    RUNNING,
    STOPPING,
    STOPPED,
    EXITED,
    FAILED
}
