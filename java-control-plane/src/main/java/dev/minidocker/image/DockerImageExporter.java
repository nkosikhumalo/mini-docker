package dev.minidocker.image;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.apache.commons.compress.archivers.tar.TarArchiveEntry;
import org.apache.commons.compress.archivers.tar.TarArchiveInputStream;
import org.apache.commons.compress.archivers.tar.TarArchiveOutputStream;
import org.apache.commons.compress.compressors.gzip.GzipCompressorInputStream;
import org.springframework.stereotype.Component;

import java.io.BufferedInputStream;
import java.io.BufferedOutputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.security.DigestOutputStream;
import java.time.Instant;
import java.util.HexFormat;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;

/** Converts MiniDock's rootfs bundle into the docker-save archive format. */
@Component
public class DockerImageExporter {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final Pattern VALID_TAG = Pattern.compile("[a-z0-9][a-z0-9._-]{0,127}");
    private static final int MAX_METADATA_BYTES = 64 * 1024;

    public void export(Path sourceArchive, String image, Path destination) throws IOException {
        if (image == null || !VALID_TAG.matcher(image).matches()) {
            throw new IllegalArgumentException("Docker export requires a lowercase image name using letters, digits, '.', '_' or '-'");
        }
        Path layer = Files.createTempFile(destination.toAbsolutePath().getParent(), ".minidock-layer-", ".tar");
        try {
            ImageSettings settings = writeLayer(sourceArchive, layer);
            byte[] layerHash = sha256(layer);
            String layerDigest = HexFormat.of().formatHex(layerHash);
            byte[] config = imageConfig(settings, layerHash);
            String configName = HexFormat.of().formatHex(sha256(config)) + ".json";
            String layerName = "layer-" + layerDigest + "/layer.tar";
            byte[] manifest = JSON.writeValueAsBytes(List.of(Map.of(
                    "Config", configName,
                    "RepoTags", List.of(image + ":latest"),
                    "Layers", List.of(layerName))));

            try (OutputStream file = new BufferedOutputStream(Files.newOutputStream(destination));
                 TarArchiveOutputStream tar = new TarArchiveOutputStream(file)) {
                tar.setLongFileMode(TarArchiveOutputStream.LONGFILE_POSIX);
                tar.setBigNumberMode(TarArchiveOutputStream.BIGNUMBER_POSIX);
                putBytes(tar, "manifest.json", manifest);
                putBytes(tar, configName, config);
                TarArchiveEntry layerDirectory = new TarArchiveEntry("layer-" + layerDigest + "/");
                layerDirectory.setMode(0755);
                tar.putArchiveEntry(layerDirectory);
                tar.closeArchiveEntry();
                TarArchiveEntry layerEntry = new TarArchiveEntry(layerName);
                layerEntry.setMode(0644);
                layerEntry.setSize(Files.size(layer));
                tar.putArchiveEntry(layerEntry);
                try (InputStream input = new BufferedInputStream(Files.newInputStream(layer))) {
                    input.transferTo(tar);
                }
                tar.closeArchiveEntry();
                tar.finish();
            }
        } catch (IOException | RuntimeException error) {
            Files.deleteIfExists(destination);
            throw error;
        } finally {
            Files.deleteIfExists(layer);
        }
    }

    private static ImageSettings writeLayer(Path source, Path layer) throws IOException {
        List<String> command = List.of();
        Integer port = null;
        try (InputStream file = new BufferedInputStream(Files.newInputStream(source));
             InputStream content = source.getFileName().toString().endsWith(".gz")
                     ? new GzipCompressorInputStream(file, true) : file;
             TarArchiveInputStream input = new TarArchiveInputStream(content);
             OutputStream raw = new BufferedOutputStream(Files.newOutputStream(layer));
             TarArchiveOutputStream output = new TarArchiveOutputStream(raw)) {
            output.setLongFileMode(TarArchiveOutputStream.LONGFILE_POSIX);
            output.setBigNumberMode(TarArchiveOutputStream.BIGNUMBER_POSIX);
            TarArchiveEntry entry;
            while ((entry = input.getNextTarEntry()) != null) {
                String name = normalizedEntryName(entry.getName());
                if (name.isEmpty()) continue;
                if (name.equals("etc/minidock/image.json")) {
                    if (entry.getSize() > MAX_METADATA_BYTES) throw new IOException("image startup metadata is too large");
                    ByteArrayOutputStream metadata = new ByteArrayOutputStream();
                    input.transferTo(metadata);
                    JsonNode node = JSON.readTree(metadata.toByteArray());
                    JsonNode commandNode = node.path("command");
                    if (commandNode.isArray()) {
                        java.util.ArrayList<String> values = new java.util.ArrayList<>();
                        for (JsonNode argument : commandNode) {
                            if (!argument.isTextual() || argument.asText().isBlank() || argument.asText().indexOf('\0') >= 0) {
                                throw new IOException("image startup metadata contains an invalid command argument");
                            }
                            values.add(argument.asText());
                        }
                        command = List.copyOf(values);
                    }
                    JsonNode portNode = node.get("container_port");
                    if (portNode != null && !portNode.isNull()) {
                        int value = portNode.asInt(-1);
                        if (value < 1 || value > 65535) throw new IOException("image startup metadata has an invalid container port");
                        port = value;
                    }
                    continue;
                }
                TarArchiveEntry copy = copyEntry(entry, name);
                output.putArchiveEntry(copy);
                if (entry.isFile()) input.transferTo(output);
                output.closeArchiveEntry();
            }
            output.finish();
        }
        return new ImageSettings(command, port);
    }

    private static String normalizedEntryName(String name) throws IOException {
        String value = name;
        while (value.startsWith("./")) value = value.substring(2);
        if (value.equals(".")) return "";
        final Path normalized;
        try {
            normalized = Path.of(value).normalize();
        } catch (RuntimeException error) {
            throw new IOException("image archive contains an invalid path: " + name, error);
        }
        if (normalized.isAbsolute() || normalized.startsWith("..")) {
            throw new IOException("image archive contains an unsafe path: " + name);
        }
        return normalized.toString().replace('\\', '/');
    }

    private static TarArchiveEntry copyEntry(TarArchiveEntry source, String name) {
        TarArchiveEntry copy = new TarArchiveEntry(name, source.getLinkFlag());
        copy.setMode(source.getMode());
        copy.setUserId(source.getLongUserId());
        copy.setGroupId(source.getLongGroupId());
        copy.setUserName(source.getUserName());
        copy.setGroupName(source.getGroupName());
        copy.setModTime(source.getModTime());
        copy.setLinkName(source.getLinkName());
        copy.setSize(source.isFile() ? source.getSize() : 0);
        copy.setDevMajor(source.getDevMajor());
        copy.setDevMinor(source.getDevMinor());
        return copy;
    }

    private static byte[] imageConfig(ImageSettings settings, byte[] layerHash) throws IOException {
        Map<String, Object> config = new java.util.LinkedHashMap<>();
        if (!settings.command().isEmpty()) {
            config.put("Entrypoint", List.of(settings.command().getFirst()));
            if (settings.command().size() > 1) config.put("Cmd", settings.command().subList(1, settings.command().size()));
        }
        if (settings.port() != null) config.put("ExposedPorts", Map.of(settings.port() + "/tcp", Map.of()));
        String architecture = switch (System.getProperty("os.arch")) {
            case "x86_64", "amd64" -> "amd64";
            case "aarch64", "arm64" -> "arm64";
            case "arm" -> "arm";
            case "ppc64le" -> "ppc64le";
            case "s390x" -> "s390x";
            default -> throw new IOException("Docker export does not recognize host architecture " + System.getProperty("os.arch"));
        };
        return JSON.writeValueAsBytes(Map.of(
                "architecture", architecture,
                "os", "linux",
                "created", Instant.now().toString(),
                "config", config,
                "container_config", Map.of("Cmd", settings.command()),
                "rootfs", Map.of("type", "layers", "diff_ids", List.of("sha256:" + HexFormat.of().formatHex(layerHash))),
                "history", List.of(Map.of("created", Instant.now().toString(), "created_by", "MiniDock rootfs export"))));
    }

    private static byte[] sha256(Path path) throws IOException {
        MessageDigest digest = digest();
        try (InputStream input = Files.newInputStream(path); DigestOutputStream sink = new DigestOutputStream(OutputStream.nullOutputStream(), digest)) {
            input.transferTo(sink);
        }
        return digest.digest();
    }

    private static byte[] sha256(byte[] value) {
        return digest().digest(value);
    }

    private static MessageDigest digest() {
        try {
            return MessageDigest.getInstance("SHA-256");
        } catch (NoSuchAlgorithmException impossible) {
            throw new IllegalStateException("SHA-256 is unavailable", impossible);
        }
    }

    private static void putBytes(TarArchiveOutputStream tar, String name, byte[] value) throws IOException {
        TarArchiveEntry entry = new TarArchiveEntry(name);
        entry.setSize(value.length);
        entry.setMode(0644);
        tar.putArchiveEntry(entry);
        tar.write(value);
        tar.closeArchiveEntry();
    }

    private record ImageSettings(List<String> command, Integer port) {}
}
