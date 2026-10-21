package dev.minidocker.state;

import dev.minidocker.service.ContainerNotFoundException;
import org.springframework.stereotype.Component;

import java.util.Comparator;
import java.util.List;
import java.util.concurrent.ConcurrentHashMap;

@Component
public class ContainerRegistry {
    private final ConcurrentHashMap<String, ContainerRecord> records = new ConcurrentHashMap<>();

    public ContainerRecord create(String id, String image, String networkIp) {
        ContainerRecord record = new ContainerRecord(id, image, networkIp);
        if (records.putIfAbsent(id, record) != null) {
            throw new IllegalStateException("container ID collision");
        }
        return record;
    }

    public ContainerRecord require(String id) {
        ContainerRecord record = records.get(id);
        if (record == null) throw new ContainerNotFoundException(id);
        return record;
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
