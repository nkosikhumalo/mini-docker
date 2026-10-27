package dev.minidocker.api;

import dev.minidocker.service.ContainerNotFoundException;
import dev.minidocker.service.ContainerService;
import dev.minidocker.image.ImageNotFoundException;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.io.IOException;
import java.util.List;
import java.util.Map;

@RestController
@RequestMapping("/api/v1/containers")
public class ContainerController {
    private final ContainerService containers;

    public ContainerController(ContainerService containers) {
        this.containers = containers;
    }

    @PostMapping
    public ResponseEntity<ContainerView> create(@RequestBody CreateContainerRequest request) throws IOException {
        return ResponseEntity.accepted().body(containers.create(request));
    }

    @GetMapping
    public List<ContainerView> list(@RequestParam(name = "all", defaultValue = "false") boolean all) {
        return all ? containers.list() : containers.listActive();
    }

    @GetMapping("/{id}")
    public ContainerView get(@PathVariable("id") String id) {
        return containers.get(id);
    }

    @GetMapping(value = "/{id}/logs", produces = MediaType.APPLICATION_OCTET_STREAM_VALUE)
    public ResponseEntity<byte[]> logs(@PathVariable("id") String id,
                                       @RequestParam(name = "offset", defaultValue = "0") long offset,
                                       @RequestParam(name = "limit", defaultValue = "1048576") long limit) {
        var chunk = containers.logs(id, offset, limit);
        return ResponseEntity.ok()
                .contentType(MediaType.APPLICATION_OCTET_STREAM)
                .header("X-MiniDock-Log-Size", Long.toString(chunk.totalSize()))
                .header("X-MiniDock-Log-Offset", Long.toString(offset))
                .body(chunk.data());
    }

    @DeleteMapping("/{id}")
    public ContainerView stop(@PathVariable("id") String id) {
        return containers.stop(id);
    }

    @ExceptionHandler(IllegalArgumentException.class)
    public ResponseEntity<Map<String, String>> badRequest(IllegalArgumentException error) {
        return ResponseEntity.badRequest().body(Map.of("error", error.getMessage()));
    }

    @ExceptionHandler(ContainerNotFoundException.class)
    public ResponseEntity<Map<String, String>> notFound(ContainerNotFoundException error) {
        return ResponseEntity.status(HttpStatus.NOT_FOUND).body(Map.of("error", error.getMessage()));
    }

    @ExceptionHandler(ImageNotFoundException.class)
    public ResponseEntity<Map<String, String>> imageNotFound(ImageNotFoundException error) {
        return ResponseEntity.status(HttpStatus.NOT_FOUND).body(Map.of("error", error.getMessage()));
    }

    @ExceptionHandler(IllegalStateException.class)
    public ResponseEntity<Map<String, String>> conflict(IllegalStateException error) {
        return ResponseEntity.status(HttpStatus.CONFLICT).body(Map.of("error", error.getMessage()));
    }

    @ExceptionHandler(IOException.class)
    public ResponseEntity<Map<String, String>> invalidImage(IOException error) {
        return ResponseEntity.status(HttpStatus.UNPROCESSABLE_ENTITY).body(Map.of("error", error.getMessage()));
    }

    @ExceptionHandler(StatusRuntimeException.class)
    public ResponseEntity<Map<String, String>> runtimeFailure(StatusRuntimeException error) {
        HttpStatus status = error.getStatus().getCode() == Status.Code.NOT_FOUND
                ? HttpStatus.NOT_FOUND : HttpStatus.BAD_GATEWAY;
        String message = error.getStatus().getDescription();
        return ResponseEntity.status(status).body(Map.of("error", message == null ? "runtime request failed" : message));
    }
}
