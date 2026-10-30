package dev.minidocker.image;

import org.springframework.beans.factory.annotation.Value;
import org.springframework.stereotype.Component;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.AtomicMoveNotSupportedException;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import java.util.regex.Pattern;

@Component
public class ImageStore {
    private static final Pattern IMAGE_NAME = Pattern.compile("[A-Za-z0-9][A-Za-z0-9._-]{0,127}");
    private final Path imagesDirectory;

    public ImageStore(@Value("${minidock.images-directory}") String imagesDirectory) {
        this.imagesDirectory = Path.of(imagesDirectory).toAbsolutePath().normalize();
    }

    public synchronized Path resolveArchive(String image) throws IOException {
        if (image == null || !IMAGE_NAME.matcher(image).matches() || image.equals(".") || image.equals("..")) {
            throw new IllegalArgumentException("image must be a simple local image name");
        }
        String base = image.endsWith(".tar.gz") ? image.substring(0, image.length() - 7)
                : image.endsWith(".tar") ? image.substring(0, image.length() - 4) : image;
        List<Path> candidates = base.equals(image)
                ? List.of(imagesDirectory.resolve(base + ".tar"), imagesDirectory.resolve(base + ".tar.gz"))
                : List.of(imagesDirectory.resolve(image));
        for (Path candidate : candidates) {
            if (!candidate.normalize().startsWith(imagesDirectory)) {
                throw new IllegalArgumentException("image path escapes the configured image directory");
            }
            if (Files.isRegularFile(candidate, LinkOption.NOFOLLOW_LINKS)) {
                return candidate;
            }
        }
        throw new ImageNotFoundException("image archive not found in " + imagesDirectory + ": " + image);
    }

    public synchronized Path registerArchive(String image, Path source) throws IOException {
        if (image == null || !IMAGE_NAME.matcher(image).matches() || image.equals(".") || image.equals("..")) {
            throw new IllegalArgumentException("image must be a simple local image name");
        }
        String lower = source.getFileName().toString().toLowerCase();
        String suffix = lower.endsWith(".tar.gz") ? ".tar.gz" : lower.endsWith(".tar") ? ".tar" : null;
        if (suffix == null) throw new IllegalArgumentException("image archive must be a .tar or .tar.gz file");
        Files.createDirectories(imagesDirectory);
        Path destination = imagesDirectory.resolve(image + suffix);
        Path temporary = Files.createTempFile(imagesDirectory, ".upload-", suffix);
        try {
            Files.copy(source, temporary, StandardCopyOption.REPLACE_EXISTING);
            try {
                Files.move(temporary, destination, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
            } catch (AtomicMoveNotSupportedException unsupported) {
                Files.move(temporary, destination, StandardCopyOption.REPLACE_EXISTING);
            }
        } catch (IOException error) {
            Files.deleteIfExists(temporary);
            throw error;
        }
        String alternateSuffix = suffix.equals(".tar") ? ".tar.gz" : ".tar";
        Files.deleteIfExists(imagesDirectory.resolve(image + alternateSuffix));
        return destination;
    }

    public List<String> list() throws IOException {
        if (!Files.isDirectory(imagesDirectory)) return List.of();
        List<String> images = new ArrayList<>();
        try (var paths = Files.list(imagesDirectory)) {
            paths.filter(path -> Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS)).map(path -> path.getFileName().toString())
                    .filter(name -> name.endsWith(".tar") || name.endsWith(".tar.gz"))
                    .map(name -> name.endsWith(".tar.gz") ? name.substring(0, name.length() - 7) : name.substring(0, name.length() - 4))
                    .distinct().sorted(Comparator.naturalOrder()).forEach(images::add);
        }
        return List.copyOf(images);
    }
}
