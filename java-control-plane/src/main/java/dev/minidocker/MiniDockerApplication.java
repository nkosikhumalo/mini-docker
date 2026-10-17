package dev.minidocker;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

/** Starts the Java control plane. API endpoints and runtime calls are not implemented yet. */
@SpringBootApplication
public class MiniDockerApplication {

    public static void main(String[] args) {
        SpringApplication.run(MiniDockerApplication.class, args);
    }
}
