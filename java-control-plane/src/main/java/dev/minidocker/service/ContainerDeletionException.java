package dev.minidocker.service;

public final class ContainerDeletionException extends RuntimeException {
    public ContainerDeletionException(String message, Throwable cause) {
        super(message, cause);
    }
}
