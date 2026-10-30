package dev.minidocker.state;

import dev.minidocker.api.ContainerView;
import dev.minidocker.runtime.RuntimeGateway;

import java.time.Instant;
import java.util.List;
import java.util.function.Consumer;

public final class ContainerRecord {
    private final String id;
    private final String image;
    private final List<String> command;
    private volatile String networkIp;
    private volatile Integer containerPort;
    private volatile Integer publishedPort;
    private volatile boolean portReady;
    private final Instant createdAt;
    private final Consumer<ContainerView> persist;
    private volatile ContainerStatus status = ContainerStatus.PREPARING;
    private volatile Instant startedAt;
    private volatile Instant finishedAt;
    private volatile Integer exitCode;
    private volatile Integer pid;
    private volatile String error;
    private volatile boolean stopRequested;
    private volatile RuntimeGateway.Execution execution;

    public ContainerRecord(String id, String image, List<String> command, String networkIp, Integer containerPort,
                           Consumer<ContainerView> persist) {
        this.id = id;
        this.image = image;
        this.command = List.copyOf(command);
        this.networkIp = networkIp;
        this.containerPort = containerPort;
        this.createdAt = Instant.now();
        this.persist = persist;
    }

    private ContainerRecord(ContainerView saved, Consumer<ContainerView> persist) {
        this.id = saved.id();
        this.image = saved.image();
        this.command = saved.command() == null ? List.of() : List.copyOf(saved.command());
        this.networkIp = saved.networkIp();
        this.containerPort = saved.containerPort();
        this.publishedPort = saved.publishedPort();
        this.portReady = saved.portReady();
        this.createdAt = saved.createdAt() == null ? Instant.now() : saved.createdAt();
        this.startedAt = saved.startedAt();
        this.finishedAt = saved.finishedAt();
        this.exitCode = saved.exitCode();
        this.pid = saved.pid();
        this.status = saved.status() == null ? ContainerStatus.FAILED : saved.status();
        this.error = saved.error();
        this.persist = persist;
    }

    public static ContainerRecord restore(ContainerView saved, Consumer<ContainerView> persist) {
        return new ContainerRecord(saved, persist);
    }

    public static ContainerRecord recovered(String id, String networkIp, int pid,
                                            RuntimeGateway.Execution execution,
                                            Consumer<ContainerView> persist) {
        Instant now = Instant.now();
        ContainerView metadata = new ContainerView(id, "<recovered>", List.of(), networkIp, pid,
                ContainerStatus.RUNNING, now, now, null, null, null, null, null, false);
        ContainerRecord record = new ContainerRecord(metadata, persist);
        record.execution = execution;
        return record;
    }

    public String id() { return id; }

    public void setPublishedPort(Integer port) {
        synchronized (this) {
            this.publishedPort = port;
            this.portReady = false;
        }
        save();
    }

    public void setPortReady(boolean ready) {
        synchronized (this) {
            if (publishedPort == null) return;
            this.portReady = ready;
        }
        save();
    }

    public boolean start(RuntimeGateway.Execution running) {
        boolean accepted;
        synchronized (this) {
            accepted = !stopRequested && status == ContainerStatus.PREPARING;
            if (accepted) {
                execution = running;
                pid = running.pid();
                startedAt = Instant.now();
                status = ContainerStatus.RUNNING;
            } else {
                status = ContainerStatus.STOPPED;
                finishedAt = Instant.now();
            }
        }
        if (!accepted) running.cancel();
        save();
        return accepted;
    }

    public void requestStop() {
        RuntimeGateway.Execution toCancel;
        synchronized (this) {
            if (status == ContainerStatus.EXITED || status == ContainerStatus.STOPPED || status == ContainerStatus.FAILED) return;
            stopRequested = true;
            status = ContainerStatus.STOPPING;
            toCancel = execution;
        }
        save();
        if (toCancel != null) toCancel.cancel();
    }

    public void completed(Integer code, Throwable failure) {
        synchronized (this) {
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
            portReady = false;
            execution = null;
        }
        save();
    }

    public void preparationFailed(Throwable failure) {
        synchronized (this) {
            finishedAt = Instant.now();
            status = ContainerStatus.FAILED;
            error = failure.getMessage();
        }
        save();
    }

    public synchronized ContainerView view() {
        return new ContainerView(id, image, command, networkIp, pid, status, createdAt, startedAt,
                finishedAt, exitCode, error, containerPort, publishedPort, portReady);
    }

    public void recoverActive(int activePid, String activeIp, RuntimeGateway.Execution running) {
        boolean stop;
        synchronized (this) {
            pid = activePid;
            if (activeIp != null && !activeIp.isBlank()) networkIp = activeIp;
            if (startedAt == null) startedAt = Instant.now();
            execution = running;
            stop = status == ContainerStatus.STOPPING || status == ContainerStatus.STOPPED || stopRequested;
            if (stop) {
                stopRequested = true;
                status = ContainerStatus.STOPPING;
            } else {
                status = ContainerStatus.RUNNING;
            }
        }
        save();
        if (stop) running.cancel();
    }

    public void recoveredExit() {
        synchronized (this) {
            if (status == ContainerStatus.PREPARING) {
                status = ContainerStatus.FAILED;
                error = "startup reconciliation found no matching runtime process";
                finishedAt = Instant.now();
                execution = null;
            } else if (status == ContainerStatus.RUNNING || status == ContainerStatus.STOPPING) {
                status = ContainerStatus.EXITED;
                finishedAt = Instant.now();
                portReady = false;
                execution = null;
            }
        }
        save();
    }

    private void save() {
        if (persist != null) persist.accept(view());
    }
}
