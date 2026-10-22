package dev.minidocker.service;

import dev.minidocker.api.ContainerView;
import dev.minidocker.api.CreateContainerRequest;
import dev.minidocker.image.ImageStore;
import dev.minidocker.image.RootfsExtractor;
import dev.minidocker.runtime.RuntimeGateway;
import dev.minidocker.state.ContainerRecord;
import dev.minidocker.state.ContainerIpAllocator;
import dev.minidocker.state.ContainerRegistry;
import jakarta.annotation.PostConstruct;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.stereotype.Service;

import java.io.IOException;
import java.math.BigDecimal;
import java.math.RoundingMode;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.PosixFilePermission;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Set;
import java.util.UUID;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

@Service
public class ContainerService {
    private static final Pattern MEMORY = Pattern.compile("(?i)^([1-9][0-9]*)(B|K|KB|KI|KIB|M|MB|MI|MIB|G|GB|GI|GIB|T|TB|TI|TIB)?$");
    private static final Pattern ENV_NAME = Pattern.compile("[A-Za-z_][A-Za-z0-9_]*");
    private static final long CPU_PERIOD = 100_000;

    private final ImageStore images;
    private final RootfsExtractor extractor;
    private final ContainerRegistry registry;
    private final ContainerIpAllocator ipAllocator;
    private final RuntimeGateway runtime;
    private final Path containersDirectory;

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
        if (!containerDirectory.startsWith(containersDirectory)) {
            throw new IllegalStateException("generated container path escaped its data directory");
        }
        ContainerRecord record;
        try {
            record = registry.create(id, validated.image(), validated.command(), networkIp);
        } catch (RuntimeException failure) {
            ipAllocator.release(id);
            throw failure;
        }
        try {
            Files.createDirectories(containersDirectory);
            Files.createDirectory(containerDirectory);
            Files.setPosixFilePermissions(containerDirectory, Set.of(
                    PosixFilePermission.OWNER_READ,
                    PosixFilePermission.OWNER_WRITE,
                    PosixFilePermission.OWNER_EXECUTE));
            extractor.extract(archive, rootfs);
            RuntimeGateway.RunConfig config = new RuntimeGateway.RunConfig(
                    id,
                    rootfs.toString(),
                    validated.command(),
                    validated.environment(),
                    validated.hostname(),
                    validated.memoryMax(),
                    validated.cpuQuota(),
                    validated.cpuPeriod(),
                    validated.network(),
                    networkIp,
                    validated.attach(),
                    validated.tty(),
                    containerDirectory.resolve("container.log").toString());
            RuntimeGateway.Execution execution = runtime.run(config);
            if (record.start(execution)) {
                execution.completion().whenComplete((result, failure) -> {
                    record.completed(result == null ? null : result.exitCode(), unwrap(failure));
                    ipAllocator.release(id);
                    try {
                        RootfsExtractor.deleteTree(rootfs);
                    } catch (IOException cleanupError) {
                        // Runtime state is already final; cleanup failures are visible in server logs.
                        System.err.println("minidock: failed to clean rootfs for " + id + ": " + cleanupError);
                    }
                });
            } else {
                ipAllocator.release(id);
                try {
                    RootfsExtractor.deleteTree(containerDirectory);
                } catch (IOException ignored) {
                    // The stopped record remains visible for the caller to inspect.
                }
            }
            return record.view();
        } catch (IOException | RuntimeException failure) {
            record.preparationFailed(failure);
            ipAllocator.release(id);
            try {
                RootfsExtractor.deleteTree(containerDirectory);
            } catch (IOException cleanupError) {
                failure.addSuppressed(cleanupError);
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
        execution.completion().whenComplete((result, failure) -> {
            record.completed(result == null ? null : result.exitCode(), unwrap(failure));
            ipAllocator.release(record.id());
            cleanupRootfs(record.id());
        });
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
        if (request.command() == null || request.command().isEmpty()
                || request.command().getFirst() == null || request.command().getFirst().isBlank()) {
            throw new IllegalArgumentException("command must include an executable");
        }
        for (String argument : request.command()) {
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
        return new ValidatedRequest(request.image(), List.copyOf(request.command()), List.copyOf(environment),
                request.hostname(), memoryMax, cpuQuota, cpuQuota == 0 ? 0 : CPU_PERIOD,
                request.network() == null || request.network(), attach, tty);
    }

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
                                    boolean network, boolean attach, boolean tty) {}
}
