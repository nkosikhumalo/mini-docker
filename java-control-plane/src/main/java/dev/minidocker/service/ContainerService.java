package dev.minidocker.service;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import dev.minidocker.api.ContainerView;
import dev.minidocker.api.CreateContainerRequest;
import dev.minidocker.image.ImageStore;
import dev.minidocker.image.RootfsExtractor;
import dev.minidocker.runtime.RuntimeGateway;
import dev.minidocker.state.ContainerRecord;
import dev.minidocker.state.ContainerIpAllocator;
import dev.minidocker.state.ContainerRegistry;
import dev.minidocker.state.ContainerStatus;
import jakarta.annotation.PreDestroy;
import jakarta.annotation.PostConstruct;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.stereotype.Service;

import java.io.IOException;
import java.net.HttpURLConnection;
import java.net.URL;
import java.math.BigDecimal;
import java.math.RoundingMode;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.attribute.PosixFilePermission;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.stream.Stream;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

@Service
public class ContainerService {
    private static final Pattern MEMORY = Pattern.compile("(?i)^([1-9][0-9]*)(B|K|KB|KI|KIB|M|MB|MI|MIB|G|GB|GI|GIB|T|TB|TI|TIB)?$");
    private static final Pattern ENV_NAME = Pattern.compile("[A-Za-z_][A-Za-z0-9_]*");
    private static final long CPU_PERIOD = 100_000;
    private static final ObjectMapper IMAGE_METADATA = new ObjectMapper();

    private final ImageStore images;
    private final RootfsExtractor extractor;
    private final ContainerRegistry registry;
    private final ContainerIpAllocator ipAllocator;
    private final RuntimeGateway runtime;
    private final Path containersDirectory;
    private final ConcurrentHashMap<String, LocalPortForwarder.Forward> publishedPorts = new ConcurrentHashMap<>();

    public ContainerService(ImageStore images, RootfsExtractor extractor, ContainerRegistry registry,
                            ContainerIpAllocator ipAllocator,
                            RuntimeGateway runtime,
                            @Value("${minidock.containers-directory}") String containersDirectory) {
        this.images = images;
        this.extractor = extractor;
        this.registry = registry;
        this.ipAllocator = ipAllocator;
        this.runtime = runtime;
        this.containersDirectory = Path.of(containersDirectory).toAbsolutePath().normalize();
    }

    public ContainerView create(CreateContainerRequest request) throws IOException {
        ValidatedRequest validated = validate(request);
        Path archive = images.resolveArchive(validated.image());
        String id = UUID.randomUUID().toString().replace("-", "").substring(0, 12);
        String networkIp = validated.network() ? ipAllocator.allocate(id) : "";
        Path containerDirectory = containersDirectory.resolve(id);
        Path rootfs = containerDirectory.resolve("rootfs");
        Path containerLog = containerDirectory.resolve("container.log");
        if (!containerDirectory.startsWith(containersDirectory)) {
            throw new IllegalStateException("generated container path escaped its data directory");
        }
        ContainerRecord record = null;
        try {
            Files.createDirectories(containersDirectory);
            Files.createDirectory(containerDirectory);
            Files.setPosixFilePermissions(containerDirectory, Set.of(
                    PosixFilePermission.OWNER_READ,
                    PosixFilePermission.OWNER_WRITE,
                    PosixFilePermission.OWNER_EXECUTE));
            extractor.extract(archive, rootfs);
            ImageRuntimeConfig imageConfig = readImageRuntimeConfig(rootfs);
            List<String> command = validated.command().isEmpty() ? imageConfig.command() : validated.command();
            if (command.isEmpty()) {
                throw new IllegalArgumentException("MiniDock could not identify this image's startup program. Rebuild it with a supported MiniDock source adapter so the image includes startup metadata.");
            }
            Integer containerPort = validated.containerPort() == null ? imageConfig.containerPort() : validated.containerPort();
            if (containerPort != null && !validated.network()) {
                throw new IllegalArgumentException("port publishing requires container networking");
            }
            ContainerRecord containerRecord = registry.create(id, validated.image(), command, networkIp,
                    containerPort == null ? null : containerPort);
            record = containerRecord;
            RuntimeGateway.RunConfig config = new RuntimeGateway.RunConfig(
                    id,
                    rootfs.toString(),
                    command,
                    validated.environment(),
                    validated.hostname(),
                    validated.memoryMax(),
                    validated.cpuQuota(),
                    validated.cpuPeriod(),
                    validated.network(),
                    networkIp,
                    validated.attach(),
                    validated.tty(),
                    containerLog.toString());
            LocalPortForwarder.Forward forward = null;
            if (containerPort != null) {
                try {
                    forward = LocalPortForwarder.open(networkIp, containerPort, null);
                    publishedPorts.put(id, forward);
                    containerRecord.setPublishedPort(forward.hostPort());
                } catch (IOException publishFailure) {
                    throw new IOException("could not publish the app port on localhost: " + publishFailure.getMessage(), publishFailure);
                }
            }
            // Reserve the host port before starting the workload. A bind error
            // must not leave a running process while the catch path removes its rootfs.
            RuntimeGateway.Execution execution = runtime.run(config);
            if (containerRecord.start(execution)) {
                if (forward != null) monitorPortReadiness(containerRecord, forward, execution);
                execution.completion().whenComplete((result, failure) -> {
                    closePublishedPort(id);
                    ipAllocator.release(id);
                    cleanupRootfs(id);
                    containerRecord.completed(result == null ? null : result.exitCode(), unwrap(failure));
                });
            } else {
                closePublishedPort(id);
                ipAllocator.release(id);
                try {
                    RootfsExtractor.deleteTree(containerDirectory);
                } catch (IOException ignored) {
                    // The stopped record remains visible for the caller to inspect.
                }
            }
            return containerRecord.view();
        } catch (IOException | RuntimeException failure) {
            closePublishedPort(id);
            ipAllocator.release(id);
            if (!(failure instanceof RuntimeGateway.StartCleanupException)) {
                try {
                    Path cleanupTarget = Files.isRegularFile(containerLog, LinkOption.NOFOLLOW_LINKS)
                            ? rootfs : containerDirectory;
                    RootfsExtractor.deleteTree(cleanupTarget);
                } catch (IOException cleanupError) {
                    failure.addSuppressed(cleanupError);
                }
            }
            if (record != null) {
                record.setPublishedPort(null);
                record.preparationFailed(failure);
            }
            throw failure;
        }
    }

    public List<ContainerView> list() {
        return registry.list();
    }

    public List<ContainerView> listActive() {
        return registry.listActive();
    }

    public ContainerView get(String id) {
        return registry.require(id).view();
    }

    public ContainerView stop(String id) {
        ContainerRecord record = registry.require(id);
        record.requestStop();
        return record.view();
    }

    public void delete(String id) {
        ContainerRecord record = registry.require(id);
        ContainerView view = record.view();
        if (view.status() != ContainerStatus.EXITED
                && view.status() != ContainerStatus.STOPPED
                && view.status() != ContainerStatus.FAILED) {
            throw new IllegalStateException("stop the container and wait for it to finish before deleting it permanently");
        }
        if (runtime.listActive().stream().anyMatch(active -> active.id().equals(record.id()))) {
            throw new IllegalStateException("the runtime still reports this container as active; refusing to remove its files");
        }
        Path directory = containersDirectory.resolve(record.id()).normalize();
        if (!directory.startsWith(containersDirectory) || directory.equals(containersDirectory)) {
            throw new IllegalStateException("container data path is outside MiniDock's containers directory");
        }
        try {
            RootfsExtractor.deleteTree(directory);
        } catch (IOException failure) {
            throw new ContainerDeletionException("could not remove container files for " + id, failure);
        }
        registry.remove(record.id());
        closePublishedPort(id);
        ipAllocator.release(record.id());
    }

    public RuntimeGateway.LogChunk logs(String id, long offset, long limit) {
        if (offset < 0 || limit < 0 || limit > 1024L * 1024) {
            throw new IllegalArgumentException("log offset must be nonnegative and limit must not exceed 1 MiB");
        }
        return runtime.logs(id, offset, limit);
    }

    @PostConstruct
    public void reconcileAfterRestart() {
        List<RuntimeGateway.ActiveContainer> active = runtime.listActive();
        Map<String, RuntimeGateway.ActiveContainer> byId = new HashMap<>();
        Map<String, String> leases = new HashMap<>();
        for (RuntimeGateway.ActiveContainer container : active) {
            byId.put(container.id(), container);
            leases.put(container.id(), container.networkIp());
        }
        ipAllocator.restoreActiveLeases(leases);

        Set<String> reconciledIds = new HashSet<>();
        for (ContainerRecord record : registry.records()) {
            RuntimeGateway.ActiveContainer running = byId.get(record.id());
            if (running == null) {
                record.recoveredExit();
                ipAllocator.release(record.id());
                cleanupRootfs(record.id());
                continue;
            }
            reconciledIds.add(record.id());
            ContainerView saved = record.view();
            if (saved.containerPort() != null && saved.publishedPort() != null && !running.networkIp().isBlank()) {
                try {
                    LocalPortForwarder.Forward forward = LocalPortForwarder.open(
                            running.networkIp(), saved.containerPort(), saved.publishedPort());
                    publishedPorts.put(record.id(), forward);
                    record.setPortReady(false);
                } catch (IOException publishFailure) {
                    record.setPublishedPort(null);
                    System.err.println("minidock: could not restore localhost port for " + record.id() + ": " + publishFailure);
                }
            }
            RuntimeGateway.Execution execution = runtime.recover(running.id(), running.pid());
            record.recoverActive(running.pid(), running.networkIp(), execution);
            monitorRecovered(record, execution);
        }
        for (RuntimeGateway.ActiveContainer orphan : active) {
            if (reconciledIds.contains(orphan.id())) continue;
            RuntimeGateway.Execution execution = runtime.recover(orphan.id(), orphan.pid());
            ContainerRecord recovered = registry.recoverUnknown(orphan, execution);
            monitorRecovered(recovered, execution);
        }
    }

    private void monitorRecovered(ContainerRecord record, RuntimeGateway.Execution execution) {
        LocalPortForwarder.Forward forward = publishedPorts.get(record.id());
        if (forward != null) monitorPortReadiness(record, forward, execution);
        execution.completion().whenComplete((result, failure) -> {
            closePublishedPort(record.id());
            ipAllocator.release(record.id());
            cleanupRootfs(record.id());
            record.completed(result == null ? null : result.exitCode(), unwrap(failure));
        });
    }

    private void monitorPortReadiness(ContainerRecord record, LocalPortForwarder.Forward forward,
                                      RuntimeGateway.Execution execution) {
        Thread.ofVirtual().start(() -> {
            while (!forward.isClosed() && !execution.completion().isDone()) {
                if (httpResponds(forward.hostPort())) {
                    if (!forward.isClosed() && !execution.completion().isDone()) {
                        record.setPortReady(true);
                    }
                    return;
                }
                try {
                    Thread.sleep(500);
                } catch (InterruptedException interrupted) {
                    Thread.currentThread().interrupt();
                    return;
                }
            }
        });
    }

    private static boolean httpResponds(int hostPort) {
        HttpURLConnection connection = null;
        try {
            connection = (HttpURLConnection) new URL("http://127.0.0.1:" + hostPort + "/").openConnection();
            connection.setConnectTimeout(500);
            connection.setReadTimeout(500);
            connection.setInstanceFollowRedirects(false);
            int status = connection.getResponseCode();
            return status >= 100 && status < 600;
        } catch (IOException notReady) {
            return false;
        } finally {
            if (connection != null) connection.disconnect();
        }
    }

    private void closePublishedPort(String id) {
        LocalPortForwarder.Forward forward = publishedPorts.remove(id);
        if (forward != null) forward.close();
    }

    @PreDestroy
    public void closePublishedPorts() {
        publishedPorts.keySet().forEach(this::closePublishedPort);
    }

    private void cleanupRootfs(String id) {
        try {
            RootfsExtractor.deleteTree(containersDirectory.resolve(id).resolve("rootfs"));
        } catch (IOException cleanupError) {
            System.err.println("minidock: failed to clean rootfs for " + id + ": " + cleanupError);
        }
    }

    private static ValidatedRequest validate(CreateContainerRequest request) {
        if (request == null) throw new IllegalArgumentException("request body is required");
        if (request.image() == null || request.image().isBlank()) {
            throw new IllegalArgumentException("image is required");
        }
        List<String> requestedCommand = request.command() == null ? List.of() : request.command();
        if (!requestedCommand.isEmpty()
                && (requestedCommand.getFirst() == null || requestedCommand.getFirst().isBlank())) {
            throw new IllegalArgumentException("command must include an executable");
        }
        for (String argument : requestedCommand) {
            if (argument == null || argument.indexOf('\0') >= 0) {
                throw new IllegalArgumentException("command arguments cannot be null or contain NUL");
            }
        }
        if (request.hostname() != null && (request.hostname().length() > 64 || request.hostname().indexOf('\0') >= 0)) {
            throw new IllegalArgumentException("hostname cannot exceed 64 characters or contain NUL");
        }
        boolean attach = Boolean.TRUE.equals(request.attach());
        boolean tty = Boolean.TRUE.equals(request.tty());
        if (tty && !attach) throw new IllegalArgumentException("tty requires attach mode");
        List<String> environment = new ArrayList<>();
        if (request.environment() != null) {
            for (Map.Entry<String, String> item : request.environment().entrySet()) {
                if (item.getKey() == null || !ENV_NAME.matcher(item.getKey()).matches()
                        || item.getValue() == null || item.getValue().indexOf('\0') >= 0) {
                    throw new IllegalArgumentException("environment entries must have valid names and non-null values");
                }
                environment.add(item.getKey() + "=" + item.getValue());
            }
        }
        Map<String, String> suppliedEnvironment = request.environment() == null ? Map.of() : request.environment();
        if (!suppliedEnvironment.containsKey("PATH")) environment.add(0, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin");
        if (!suppliedEnvironment.containsKey("HOME")) environment.add("HOME=/root");
        if (tty && !suppliedEnvironment.containsKey("TERM")) environment.add("TERM=xterm-256color");
        long memoryMax = parseMemory(request.memoryLimit());
        long cpuQuota = parseCPUQuota(request.cpuLimit());
        Integer containerPort = request.containerPort();
        if (containerPort != null && (containerPort < 1 || containerPort > 65535)) {
            throw new IllegalArgumentException("container_port must be between 1 and 65535");
        }
        boolean network = request.network() == null || request.network();
        if (containerPort != null && !network) {
            throw new IllegalArgumentException("port publishing requires container networking");
        }
        return new ValidatedRequest(request.image(), List.copyOf(requestedCommand), List.copyOf(environment),
                request.hostname(), memoryMax, cpuQuota, cpuQuota == 0 ? 0 : CPU_PERIOD,
                network, attach, tty, containerPort);
    }

    private static ImageRuntimeConfig readImageRuntimeConfig(Path rootfs) throws IOException {
        Path metadata = rootfs.resolve("etc/minidock/image.json").normalize();
        if (metadata.startsWith(rootfs) && Files.isRegularFile(metadata, LinkOption.NOFOLLOW_LINKS)) {
            if (Files.size(metadata) > 64 * 1024) throw new IOException("image startup metadata is too large");
            JsonNode node = IMAGE_METADATA.readTree(Files.readAllBytes(metadata));
            JsonNode commandNode = node.path("command");
            if (!commandNode.isArray() || commandNode.isEmpty()) throw new IOException("image startup metadata has no command");
            List<String> command = new ArrayList<>();
            for (JsonNode argument : commandNode) {
                if (!argument.isTextual() || argument.asText().isBlank() || argument.asText().indexOf('\0') >= 0) {
                    throw new IOException("image startup metadata contains an invalid command argument");
                }
                command.add(argument.asText());
            }
            JsonNode portNode = node.get("container_port");
            Integer port = portNode == null || portNode.isNull() ? null : portNode.asInt(-1);
            if (port != null && (port < 1 || port > 65535)) throw new IOException("image startup metadata has an invalid container port");
            return new ImageRuntimeConfig(List.copyOf(command), port);
        }

        Path web = rootfs.resolve("usr/bin/minidock-web");
        if (Files.isRegularFile(web, LinkOption.NOFOLLOW_LINKS)
                && Files.isExecutable(web)
                && Files.isRegularFile(rootfs.resolve("usr/share/minidock/www/index.html"), LinkOption.NOFOLLOW_LINKS)) {
            return new ImageRuntimeConfig(List.of("/usr/bin/minidock-web", "--root", "/usr/share/minidock/www",
                    "--listen", "0.0.0.0:8080"), 8080);
        }
        Path java = rootfs.resolve("usr/lib/minidock-jre/bin/java");
        Path jar = rootfs.resolve("opt/minidock/app/app.jar");
        if (Files.isRegularFile(java, LinkOption.NOFOLLOW_LINKS) && Files.isExecutable(java)
                && Files.isRegularFile(jar, LinkOption.NOFOLLOW_LINKS)) {
            return new ImageRuntimeConfig(List.of("/usr/lib/minidock-jre/bin/java", "-jar", "/opt/minidock/app/app.jar"), 8080);
        }
        Path binaries = rootfs.resolve("usr/bin");
        if (Files.isDirectory(binaries, LinkOption.NOFOLLOW_LINKS)) {
            try (Stream<Path> entries = Files.list(binaries)) {
                List<Path> executables = entries.filter(path -> Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS))
                        .filter(Files::isExecutable).toList();
                if (executables.size() == 1) {
                    String relative = rootfs.relativize(executables.getFirst()).toString().replace('\\', '/');
                    return new ImageRuntimeConfig(List.of("/" + relative), null);
                }
            }
        }
        return new ImageRuntimeConfig(List.of(), null);
    }

    private record ImageRuntimeConfig(List<String> command, Integer containerPort) {}

    private static long parseMemory(String input) {
        if (input == null || input.isBlank()) return 0;
        Matcher matcher = MEMORY.matcher(input.trim());
        if (!matcher.matches()) {
            throw new IllegalArgumentException("memory_limit must look like 256M, 512MiB, or a byte count");
        }
        long value;
        try {
            value = Long.parseLong(matcher.group(1));
        } catch (NumberFormatException error) {
            throw new IllegalArgumentException("memory_limit is too large");
        }
        String suffix = matcher.group(2) == null ? "B" : matcher.group(2).toUpperCase();
        int power = switch (suffix) {
            case "B" -> 0;
            case "K", "KB", "KI", "KIB" -> 1;
            case "M", "MB", "MI", "MIB" -> 2;
            case "G", "GB", "GI", "GIB" -> 3;
            case "T", "TB", "TI", "TIB" -> 4;
            default -> throw new IllegalArgumentException("unsupported memory unit: " + suffix);
        };
        try {
            for (int i = 0; i < power; i++) value = Math.multiplyExact(value, 1024);
        } catch (ArithmeticException error) {
            throw new IllegalArgumentException("memory_limit is too large");
        }
        return value;
    }

    private static long parseCPUQuota(String input) {
        if (input == null || input.isBlank()) return 0;
        String value = input.trim();
        boolean percent = value.endsWith("%");
        if (percent) value = value.substring(0, value.length() - 1).trim();
        try {
            BigDecimal cpus = new BigDecimal(value);
            if (percent) cpus = cpus.divide(BigDecimal.valueOf(100), 8, RoundingMode.HALF_UP);
            if (cpus.signum() <= 0 || cpus.compareTo(BigDecimal.valueOf(256)) > 0) {
                throw new IllegalArgumentException("cpu_limit must be greater than 0 and at most 256 CPUs");
            }
            return cpus.multiply(BigDecimal.valueOf(CPU_PERIOD)).setScale(0, RoundingMode.CEILING).longValueExact();
        } catch (NumberFormatException | ArithmeticException error) {
            throw new IllegalArgumentException("cpu_limit must be a positive CPU count or percentage, such as 0.5 or 50%");
        }
    }

    private static Throwable unwrap(Throwable error) {
        if (error instanceof java.util.concurrent.CompletionException && error.getCause() != null) return error.getCause();
        return error;
    }

    private record ValidatedRequest(String image, List<String> command, List<String> environment,
                                    String hostname, long memoryMax, long cpuQuota, long cpuPeriod,
                                    boolean network, boolean attach, boolean tty, Integer containerPort) {}
}
