package dev.minidocker.api;

import dev.minidocker.state.ContainerStatus;

import java.time.Instant;

public record ContainerView(
        String id,
        String image,
        String networkIp,
        Integer pid,
        ContainerStatus status,
        Instant createdAt,
        Instant startedAt,
        Instant finishedAt,
        Integer exitCode,
        String error) {
}
