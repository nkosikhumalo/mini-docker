package dev.minidocker.state;

import org.springframework.stereotype.Component;

import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.Set;

/** Java-side lease registry; Go applies the assigned address to the veth. */
@Component
public class ContainerIpAllocator {
    private final Map<String, String> byContainer = new HashMap<>();
    private final Set<String> allocated = new HashSet<>();
    private int nextHost = 2;

    public synchronized String allocate(String containerId) {
        if (byContainer.containsKey(containerId)) return byContainer.get(containerId);
        for (int checked = 0; checked < 253; checked++) {
            int host = nextHost++;
            if (nextHost > 254) nextHost = 2;
            String candidate = "10.0.0." + host;
            if (allocated.add(candidate)) {
                byContainer.put(containerId, candidate);
                return candidate;
            }
        }
        throw new IllegalStateException("MiniDock bridge subnet has no free container IP addresses");
    }

    public synchronized void release(String containerId) {
        String address = byContainer.remove(containerId);
        if (address != null) allocated.remove(address);
    }
}
