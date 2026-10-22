package dev.minidocker.runtime;

import java.util.List;
import java.util.concurrent.CompletableFuture;

public interface RuntimeGateway {
    Execution run(RunConfig config);
    List<ActiveContainer> listActive();
    LogChunk logs(String id, long offset, long limit);
    Execution recover(String id, int pid);

    record RunConfig(String id, String rootfs, List<String> command, List<String> environment,
                     String hostname, long memoryMax, long cpuQuota, long cpuPeriod, boolean network,
                     String networkIp, boolean attach, boolean tty, String logPath) {}

    record RunResult(int exitCode) {}
    record ActiveContainer(String id, int pid, String networkIp) {}
    record LogChunk(byte[] data, long totalSize) {}

    interface Execution {
        int pid();
        CompletableFuture<RunResult> completion();
        void cancel();
    }
}
