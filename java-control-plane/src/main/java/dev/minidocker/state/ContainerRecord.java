package dev.minidocker.state;

import dev.minidocker.api.ContainerView;
import dev.minidocker.runtime.RuntimeGateway;

import java.time.Instant;

public final class ContainerRecord {
    private final String id;
    private final String image;
    private final String networkIp;
    private final Instant createdAt = Instant.now();
    private volatile ContainerStatus status = ContainerStatus.PREPARING;
    private volatile Instant startedAt;
    private volatile Instant finishedAt;
    private volatile Integer exitCode;
    private volatile Integer pid;
    private volatile String error;
    private volatile boolean stopRequested;
    private volatile RuntimeGateway.Execution execution;

    public ContainerRecord(String id, String image, String networkIp) {
        this.id = id;
        this.image = image;
        this.networkIp = networkIp;
    }

    public String id() { return id; }

    public synchronized boolean start(RuntimeGateway.Execution running) {
        if (stopRequested || status != ContainerStatus.PREPARING) {
            running.cancel();
            status = ContainerStatus.STOPPED;
            finishedAt = Instant.now();
            return false;
        }
        execution = running;
        pid = running.pid();
        startedAt = Instant.now();
        status = ContainerStatus.RUNNING;
        return true;
    }

    public synchronized void requestStop() {
        if (status == ContainerStatus.EXITED || status == ContainerStatus.STOPPED || status == ContainerStatus.FAILED) {
            return;
        }
        stopRequested = true;
        status = ContainerStatus.STOPPING;
        if (execution != null) execution.cancel();
    }

    public synchronized void completed(Integer code, Throwable failure) {
        finishedAt = Instant.now();
        if (stopRequested) {
            status = ContainerStatus.STOPPED;
        } else if (failure != null) {
            status = ContainerStatus.FAILED;
            error = failure.getMessage();
        } else {
            status = ContainerStatus.EXITED;
            exitCode = code;
        }
        execution = null;
    }

    public synchronized void preparationFailed(Throwable failure) {
        finishedAt = Instant.now();
        status = ContainerStatus.FAILED;
        error = failure.getMessage();
    }

    public synchronized ContainerView view() {
        return new ContainerView(id, image, networkIp, pid, status, createdAt, startedAt, finishedAt, exitCode, error);
    }
}
