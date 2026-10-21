package dev.minidocker;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

/** Starts the MiniDock Java control plane. */
@SpringBootApplication
public class MiniDockerApplication {

    public static void main(String[] args) {
        SpringApplication.run(MiniDockerApplication.class, args);
    }
}
