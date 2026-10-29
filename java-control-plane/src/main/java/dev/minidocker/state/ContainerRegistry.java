package dev.minidocker.state;

import dev.minidocker.service.ContainerNotFoundException;
import dev.minidocker.api.ContainerView;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import dev.minidocker.runtime.RuntimeGateway;
import jakarta.annotation.PreDestroy;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.stereotype.Component;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.channels.FileChannel;
import java.nio.channels.FileLock;
import java.nio.channels.OverlappingFileLockException;
import java.nio.ByteBuffer;
import java.util.Comparator;
import java.util.List;
import java.nio.file.AtomicMoveNotSupportedException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.nio.file.StandardCopyOption;
import java.nio.file.attribute.PosixFilePermission;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;

@Component
public class ContainerRegistry {
    // Concurrent map protects record publication/lookups; ContainerRecord
    // synchronizes lifecycle transitions and publishes snapshots via view().
    private final ConcurrentHashMap<String, ContainerRecord> records = new ConcurrentHashMap<>();
    private final ObjectMapper mapper;
    private final Path stateFile;
    private FileChannel lockChannel;
    private FileLock stateLock;

    public ContainerRegistry(ObjectMapper mapper,
                             @Value("${minidock.state-file}") String stateFile) {
        this.mapper = mapper;
        this.stateFile = Path.of(stateFile).toAbsolutePath().normalize();
        try {
            acquireSingleInstanceLock();
            load();
        } catch (IOException | RuntimeException failure) {
            releaseSingleInstanceLock();
            throw new IllegalStateException("could not initialize MiniDock state at " + this.stateFile, failure);
        }
    }

    public ContainerRecord create(String id, String image, List<String> command, String networkIp, Integer containerPort) {
        ContainerRecord record = new ContainerRecord(id, image, command, networkIp, containerPort, ignored -> persist());
        if (records.putIfAbsent(id, record) != null) {
            throw new IllegalStateException("container ID collision");
        }
        try {
            persist();
        } catch (RuntimeException failure) {
            records.remove(id, record);
            throw failure;
        }
        return record;
    }

    public ContainerRecord require(String id) {
        ContainerRecord record = records.get(id);
        if (record == null) throw new ContainerNotFoundException(id);
        return record;
    }

    public ContainerRecord find(String id) { return records.get(id); }

    public void remove(String id) {
        ContainerRecord record = require(id);
        if (!records.remove(id, record)) throw new ContainerNotFoundException(id);
        try {
            persist();
        } catch (RuntimeException failure) {
            records.putIfAbsent(id, record);
            throw failure;
        }
    }

    public ContainerRecord recoverUnknown(RuntimeGateway.ActiveContainer active, RuntimeGateway.Execution execution) {
        ContainerRecord recovered = ContainerRecord.recovered(active.id(), active.networkIp(), active.pid(), execution,
                ignored -> persist());
        ContainerRecord existing = records.putIfAbsent(active.id(), recovered);
        if (existing != null) return existing;
        persist();
        return recovered;
    }

    public List<ContainerRecord> records() { return List.copyOf(records.values()); }

    private void load() {
        if (!Files.exists(stateFile)) return;
        try {
            List<ContainerView> saved = mapper.readValue(stateFile.toFile(), new TypeReference<>() {});
            for (ContainerView view : saved) {
                if (view.id() == null || view.id().isBlank()) continue;
                records.putIfAbsent(view.id(), ContainerRecord.restore(view, ignored -> persist()));
            }
        } catch (IOException | RuntimeException failure) {
            throw new IllegalStateException("could not load MiniDock state from " + stateFile, failure);
        }
    }

    private void acquireSingleInstanceLock() throws IOException {
        Path parent = stateFile.getParent();
        Files.createDirectories(parent);
        Path lockFile = stateFile.resolveSibling(stateFile.getFileName() + ".lock");
        lockChannel = FileChannel.open(lockFile, StandardOpenOption.CREATE, StandardOpenOption.WRITE);
        try {
            Files.setPosixFilePermissions(lockFile, Set.of(
                    PosixFilePermission.OWNER_READ, PosixFilePermission.OWNER_WRITE));
        } catch (UnsupportedOperationException ignored) {
            // POSIX permissions are available on the Linux hosts MiniDock targets.
        }
        try {
            stateLock = lockChannel.tryLock();
        } catch (OverlappingFileLockException alreadyLockedInThisJvm) {
            throw new IOException("another MiniDock control plane already owns " + stateFile, alreadyLockedInThisJvm);
        }
        if (stateLock == null) {
            throw new IOException("another MiniDock control plane already owns " + stateFile);
        }
    }

    @PreDestroy
    public void releaseSingleInstanceLock() {
        try {
            if (stateLock != null && stateLock.isValid()) stateLock.release();
        } catch (IOException failure) {
            System.err.println("minidock: could not release state lock: " + failure);
        } finally {
            stateLock = null;
        }
        try {
            if (lockChannel != null && lockChannel.isOpen()) lockChannel.close();
        } catch (IOException failure) {
            System.err.println("minidock: could not close state lock file: " + failure);
        } finally {
            lockChannel = null;
        }
    }

    private synchronized void persist() {
        Path parent = stateFile.getParent();
        Path temporary = null;
        try {
            Files.createDirectories(parent);
            temporary = Files.createTempFile(parent, ".minidock-state-", ".json.tmp");
            try {
                Files.setPosixFilePermissions(temporary, Set.of(PosixFilePermission.OWNER_READ, PosixFilePermission.OWNER_WRITE));
            } catch (UnsupportedOperationException ignored) {
                // The runtime targets Linux; keep state usable on non-POSIX development filesystems too.
            }
            byte[] snapshot = mapper.writeValueAsBytes(records.values().stream().map(ContainerRecord::view).toList());
            try (FileChannel channel = FileChannel.open(temporary, StandardOpenOption.WRITE, StandardOpenOption.TRUNCATE_EXISTING)) {
                ByteBuffer data = ByteBuffer.wrap(snapshot);
                while (data.hasRemaining()) channel.write(data);
                channel.force(true);
            }
            try {
                Files.move(temporary, stateFile, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
            } catch (AtomicMoveNotSupportedException unsupported) {
                throw new IOException("state directory does not support atomic replacement: " + parent, unsupported);
            }
        } catch (IOException failure) {
            throw new UncheckedIOException("could not persist MiniDock state to " + stateFile, failure);
        } finally {
            if (temporary != null) {
                try { Files.deleteIfExists(temporary); } catch (IOException ignored) { }
            }
        }
    }

    public List<dev.minidocker.api.ContainerView> list() {
        return records.values().stream().map(ContainerRecord::view)
                .sorted(Comparator.comparing(dev.minidocker.api.ContainerView::createdAt).reversed()).toList();
    }

    public List<dev.minidocker.api.ContainerView> listActive() {
        return records.values().stream().filter(record -> record.view().status() == ContainerStatus.PREPARING
                        || record.view().status() == ContainerStatus.RUNNING
                        || record.view().status() == ContainerStatus.STOPPING)
                .map(ContainerRecord::view)
                .sorted(Comparator.comparing(dev.minidocker.api.ContainerView::createdAt).reversed()).toList();
    }
}
