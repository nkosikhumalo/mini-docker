package dev.minidocker.api;

import dev.minidocker.image.ImageStore;
import org.springframework.http.MediaType;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.multipart.MultipartFile;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;

@RestController
@RequestMapping("/api/v1/images")
public class ImageController {
    private final ImageStore images;

    public ImageController(ImageStore images) {
        this.images = images;
    }

    @PostMapping(consumes = MediaType.MULTIPART_FORM_DATA_VALUE)
    public ResponseEntity<Map<String, String>> stage(@RequestParam("name") String name,
                                                     @RequestParam("archive") MultipartFile archive) throws IOException {
        if (archive.isEmpty()) throw new IllegalArgumentException("image archive must not be empty");
        String filename = archive.getOriginalFilename();
        if (filename == null) throw new IllegalArgumentException("uploaded archive needs a filename");
        String lowerName = filename.toLowerCase();
        String suffix = lowerName.endsWith(".tar.gz") ? ".tar.gz" : lowerName.endsWith(".tar") ? ".tar" : null;
        if (suffix == null) throw new IllegalArgumentException("image archive must be a .tar or .tar.gz file");
        Path temporary = Files.createTempFile("minidock-image-upload-", suffix);
        try {
            try (var input = archive.getInputStream()) {
                Files.copy(input, temporary, java.nio.file.StandardCopyOption.REPLACE_EXISTING);
            }
            Path staged = images.registerArchive(name, temporary);
            return ResponseEntity.ok(Map.of("name", name, "archive", staged.getFileName().toString()));
        } finally {
            Files.deleteIfExists(temporary);
        }
    }

    @GetMapping
    public List<String> list() throws IOException {
        return images.list();
    }

    @ExceptionHandler(IllegalArgumentException.class)
    public ResponseEntity<Map<String, String>> badRequest(IllegalArgumentException error) {
        return ResponseEntity.badRequest().body(Map.of("error", error.getMessage()));
    }

    @ExceptionHandler(IOException.class)
    public ResponseEntity<Map<String, String>> storageFailure(IOException error) {
        return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).body(Map.of("error", error.getMessage()));
    }
}
