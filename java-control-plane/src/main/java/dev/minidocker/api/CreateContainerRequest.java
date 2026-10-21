package dev.minidocker.api;

import com.fasterxml.jackson.annotation.JsonProperty;

import java.util.List;
import java.util.Map;

public record CreateContainerRequest(
        String image,
        List<String> command,
        @JsonProperty("memory_limit") String memoryLimit,
        @JsonProperty("cpu_limit") String cpuLimit,
        Map<String, String> environment,
        String hostname,
        Boolean network,
        Boolean attach,
        Boolean tty) {
}
