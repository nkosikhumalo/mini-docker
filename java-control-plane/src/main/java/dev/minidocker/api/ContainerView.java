package dev.minidocker.api;

import dev.minidocker.state.ContainerStatus;

import java.time.Instant;
import java.util.List;

public record ContainerView(
        String id,
        String image,
        List<String> command,
        String networkIp,
        Integer pid,
        ContainerStatus status,
        Instant createdAt,
        Instant startedAt,
        Instant finishedAt,
        Integer exitCode,
        String error,
        Integer containerPort,
        Integer publishedPort) {
}
