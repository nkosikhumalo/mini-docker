package dev.minidocker.image;

import org.apache.commons.compress.archivers.tar.TarArchiveEntry;
import org.apache.commons.compress.archivers.tar.TarArchiveInputStream;
import org.apache.commons.compress.compressors.gzip.GzipCompressorInputStream;
import org.springframework.stereotype.Component;

import java.io.BufferedInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.nio.file.attribute.PosixFilePermission;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.EnumSet;
import java.util.List;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Set;

@Component
public class RootfsExtractor {
    private static final long MAX_EXPANDED_BYTES = 8L * 1024 * 1024 * 1024;
    private static final int MAX_ENTRIES = 250_000;

    public void extract(Path archive, Path destination) throws IOException {
        Path root = destination.toAbsolutePath().normalize();
        if (Files.exists(root, LinkOption.NOFOLLOW_LINKS)) {
            throw new IOException("rootfs destination already exists: " + root);
        }
        Files.createDirectories(root);
        Map<Path, Integer> directories = new LinkedHashMap<>();
        long expanded = 0;
        int entryCount = 0;
        try (InputStream file = new BufferedInputStream(Files.newInputStream(archive));
             InputStream content = archive.getFileName().toString().endsWith(".gz")
                     ? new GzipCompressorInputStream(file, true) : file;
             TarArchiveInputStream tar = new TarArchiveInputStream(content)) {
            TarArchiveEntry entry;
            while ((entry = tar.getNextTarEntry()) != null) {
                if (++entryCount > MAX_ENTRIES) {
                    throw new IOException("image contains more than " + MAX_ENTRIES + " archive entries");
                }
                Path output = resolveEntry(root, entry.getName());
                if (output.equals(root)) {
                    if (!entry.isDirectory()) {
                        throw new IOException("rootfs archive root entry must be a directory");
                    }
                    directories.put(root, entry.getMode());
                    continue;
                }
                ensureSafeParents(root, output.getParent());
                if (entry.isDirectory()) {
                    ensureDirectory(root, output);
                    directories.put(output, entry.getMode());
                } else if (entry.isSymbolicLink()) {
                    ensureAbsent(output);
                    try {
                        Files.createSymbolicLink(output, Path.of(entry.getLinkName()));
                    } catch (RuntimeException error) {
                        throw new IOException("invalid symbolic link target for " + entry.getName(), error);
                    }
                } else if (entry.isLink()) {
                    Path target = resolveEntry(root, entry.getLinkName());
                    ensureSafeParents(root, target.getParent());
                    if (!Files.isRegularFile(target, LinkOption.NOFOLLOW_LINKS)) {
                        throw new IOException("tar hard link target is not a regular file: " + entry.getLinkName());
                    }
                    ensureAbsent(output);
                    Files.createLink(output, target);
                } else if (entry.isFile()) {
                    ensureAbsent(output);
                    long declaredSize = entry.getSize();
                    if (declaredSize < 0 || declaredSize > MAX_EXPANDED_BYTES - expanded) {
                        throw new IOException("image expands beyond the 8 GiB extraction limit");
                    }
                    try (OutputStream out = Files.newOutputStream(output, StandardOpenOption.CREATE_NEW,
                            StandardOpenOption.WRITE, LinkOption.NOFOLLOW_LINKS)) {
                        byte[] buffer = new byte[64 * 1024];
                        int count;
                        long written = 0;
                        while ((count = tar.read(buffer)) != -1) {
                            written += count;
                            expanded += count;
                            if (expanded > MAX_EXPANDED_BYTES || written > declaredSize) {
                                throw new IOException("image expands beyond its declared size or extraction limit");
                            }
                            out.write(buffer, 0, count);
                        }
                        if (written != declaredSize) {
                            throw new IOException("truncated tar entry: " + entry.getName());
                        }
                    }
                    setPermissions(output, entry.getMode());
                } else {
                    throw new IOException("unsupported tar entry type for " + entry.getName());
                }
            }
            List<Path> directoryPaths = new ArrayList<>(directories.keySet());
            directoryPaths.sort(Comparator.comparingInt(Path::getNameCount).reversed());
            for (Path directory : directoryPaths) {
                setPermissions(directory, directories.get(directory));
            }
        } catch (IOException | RuntimeException error) {
            try {
                deleteTree(root);
            } catch (IOException cleanupError) {
                error.addSuppressed(cleanupError);
            }
            throw error;
        }
    }

    private static Path resolveEntry(Path root, String name) throws IOException {
        final Path entry;
        try {
            entry = Path.of(name);
        } catch (RuntimeException error) {
            throw new IOException("invalid tar path: " + name, error);
        }
        Path resolved = root.resolve(entry).normalize();
        if (!resolved.startsWith(root)) {
            throw new IOException("tar path escapes rootfs: " + name);
        }
        return resolved;
    }

    private static void ensureSafeParents(Path root, Path parent) throws IOException {
        if (parent == null || !parent.startsWith(root)) {
            throw new IOException("tar entry has an unsafe parent directory");
        }
        Path current = root;
        for (Path component : root.relativize(parent)) {
            current = current.resolve(component);
            if (Files.exists(current, LinkOption.NOFOLLOW_LINKS)) {
                if (Files.isSymbolicLink(current) || !Files.isDirectory(current, LinkOption.NOFOLLOW_LINKS)) {
                    throw new IOException("tar entry traverses a non-directory or symbolic link: " + current);
                }
            } else {
                Files.createDirectory(current);
            }
        }
    }

    private static void ensureDirectory(Path root, Path directory) throws IOException {
        if (directory.equals(root)) {
            return;
        }
        if (Files.exists(directory, LinkOption.NOFOLLOW_LINKS)) {
            if (Files.isSymbolicLink(directory) || !Files.isDirectory(directory, LinkOption.NOFOLLOW_LINKS)) {
                throw new IOException("tar directory conflicts with an existing entry: " + directory);
            }
        } else {
            Files.createDirectory(directory);
        }
    }

    private static void ensureAbsent(Path path) throws IOException {
        if (Files.exists(path, LinkOption.NOFOLLOW_LINKS)) {
            throw new IOException("duplicate or conflicting tar entry: " + path);
        }
    }

    private static void setPermissions(Path path, int mode) throws IOException {
        int safeMode = mode & 0777;
        Set<PosixFilePermission> permissions = EnumSet.noneOf(PosixFilePermission.class);
        if ((safeMode & 0400) != 0) permissions.add(PosixFilePermission.OWNER_READ);
        if ((safeMode & 0200) != 0) permissions.add(PosixFilePermission.OWNER_WRITE);
        if ((safeMode & 0100) != 0) permissions.add(PosixFilePermission.OWNER_EXECUTE);
        if ((safeMode & 0040) != 0) permissions.add(PosixFilePermission.GROUP_READ);
        if ((safeMode & 0020) != 0) permissions.add(PosixFilePermission.GROUP_WRITE);
        if ((safeMode & 0010) != 0) permissions.add(PosixFilePermission.GROUP_EXECUTE);
        if ((safeMode & 0004) != 0) permissions.add(PosixFilePermission.OTHERS_READ);
        if ((safeMode & 0002) != 0) permissions.add(PosixFilePermission.OTHERS_WRITE);
        if ((safeMode & 0001) != 0) permissions.add(PosixFilePermission.OTHERS_EXECUTE);
        Files.setPosixFilePermissions(path, permissions);
    }

    public static void deleteTree(Path root) throws IOException {
        if (!Files.exists(root, LinkOption.NOFOLLOW_LINKS)) {
            return;
        }
        if (Files.isDirectory(root, LinkOption.NOFOLLOW_LINKS) && !Files.isSymbolicLink(root)) {
            setPermissions(root, 0700);
            try (var children = Files.list(root)) {
                for (Path child : children.toList()) deleteTree(child);
            }
        }
        Files.deleteIfExists(root);
    }
}
