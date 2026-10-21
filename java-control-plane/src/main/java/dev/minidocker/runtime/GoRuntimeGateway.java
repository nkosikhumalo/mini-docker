package dev.minidocker.runtime;

import dev.minidocker.runtime.v1.RunRequest;
import dev.minidocker.runtime.v1.RunResponse;
import dev.minidocker.runtime.v1.StopRequest;
import dev.minidocker.runtime.v1.WaitRequest;
import dev.minidocker.runtime.v1.RuntimeServiceGrpc;
import io.grpc.ManagedChannel;
import io.grpc.StatusRuntimeException;
import dev.minidocker.runtime.v1.StartResponse;
import io.grpc.netty.shaded.io.grpc.netty.NettyChannelBuilder;
import io.grpc.netty.shaded.io.netty.channel.EventLoopGroup;
import io.grpc.netty.shaded.io.netty.channel.epoll.Epoll;
import io.grpc.netty.shaded.io.netty.channel.epoll.EpollDomainSocketChannel;
import io.grpc.netty.shaded.io.netty.channel.epoll.EpollEventLoopGroup;
import io.grpc.netty.shaded.io.netty.channel.unix.DomainSocketAddress;
import jakarta.annotation.PreDestroy;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.stereotype.Component;

import java.nio.file.Path;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

@Component
public class GoRuntimeGateway implements RuntimeGateway {
    private static final Logger log = LoggerFactory.getLogger(GoRuntimeGateway.class);

    private final EventLoopGroup eventLoopGroup;
    private final ManagedChannel channel;

    public GoRuntimeGateway(@Value("${minidock.runtime-socket}") String socketPath) {
        if (!Epoll.isAvailable()) {
            throw new IllegalStateException("gRPC Unix sockets require Linux epoll: " + Epoll.unavailabilityCause());
        }
        this.eventLoopGroup = new EpollEventLoopGroup(1);
        this.channel = NettyChannelBuilder
                .forAddress(new DomainSocketAddress(Path.of(socketPath).toAbsolutePath().normalize().toString()))
                .channelType(EpollDomainSocketChannel.class)
                .eventLoopGroup(eventLoopGroup)
                .usePlaintext()
                .build();
    }

    @Override
    public Execution run(RunConfig config) {
        RunRequest request = RunRequest.newBuilder()
                .setId(config.id())
                .setRootfs(config.rootfs())
                .addAllCommand(config.command())
                .addAllEnv(config.environment())
                .setHostname(config.hostname() == null ? "" : config.hostname())
                .setMemoryMax(config.memoryMax())
                .setCpuQuota(config.cpuQuota())
                .setCpuPeriod(config.cpuPeriod())
                .setNetwork(config.network())
                .setNetworkIp(config.networkIp())
                .build();
        StartResponse started;
        try {
            started = RuntimeServiceGrpc.newBlockingStub(channel)
                    .withDeadlineAfter(30, TimeUnit.SECONDS).start(request);
        } catch (StatusRuntimeException error) {
            throw new IllegalStateException("Go runtime failed to start container " + config.id() + ": "
                    + error.getStatus().getDescription(), error);
        }
        CompletableFuture<RunResult> completion = CompletableFuture.supplyAsync(() -> {
            RunResponse response = RuntimeServiceGrpc.newBlockingStub(channel)
                    .wait(WaitRequest.newBuilder().setId(config.id()).build());
            return new RunResult(response.getExitCode());
        });
        return new Execution() {
            @Override
            public int pid() {
                return started.getPid();
            }

            @Override
            public CompletableFuture<RunResult> completion() {
                return completion;
            }

            @Override
            public void cancel() {
                try {
                    RuntimeServiceGrpc.newBlockingStub(channel)
                            .withDeadlineAfter(5, TimeUnit.SECONDS)
                            .stop(StopRequest.newBuilder().setId(config.id()).build());
                } catch (StatusRuntimeException error) {
                    log.warn("Go runtime failed to stop container {}", config.id(), error);
                }
            }
        };
    }

    @PreDestroy
    public void close() {
        channel.shutdownNow();
        try {
            channel.awaitTermination(2, TimeUnit.SECONDS);
        } catch (InterruptedException error) {
            Thread.currentThread().interrupt();
        }
        eventLoopGroup.shutdownGracefully();
    }
}
