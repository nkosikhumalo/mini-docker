package dev.minidocker.runtime;

import java.util.List;
import java.util.concurrent.CompletableFuture;

public interface RuntimeGateway {
    Execution run(RunConfig config);

    record RunConfig(String id, String rootfs, List<String> command, List<String> environment,
                     String hostname, long memoryMax, long cpuQuota, long cpuPeriod, boolean network,
                     String networkIp, boolean attach, boolean tty) {}

    record RunResult(int exitCode) {}

    interface Execution {
        int pid();
        CompletableFuture<RunResult> completion();
        void cancel();
    }
}
