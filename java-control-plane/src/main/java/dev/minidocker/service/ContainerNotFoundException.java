package dev.minidocker.service;

public class ContainerNotFoundException extends RuntimeException {
    public ContainerNotFoundException(String id) {
        super("container not found: " + id);
    }
}
