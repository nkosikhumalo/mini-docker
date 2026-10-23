package dev.minidocker.api;

import org.springframework.boot.web.server.ConfigurableWebServerFactory;
import org.springframework.boot.web.server.WebServerFactoryCustomizer;
import org.springframework.core.Ordered;
import org.springframework.core.annotation.Order;
import org.springframework.stereotype.Component;

import java.net.InetAddress;
import java.net.UnknownHostException;

/** Keeps the local control API off externally reachable network interfaces. */
@Component
@Order(Ordered.LOWEST_PRECEDENCE)
public class LoopbackOnlyWebServerCustomizer
        implements WebServerFactoryCustomizer<ConfigurableWebServerFactory> {

    @Override
    public void customize(ConfigurableWebServerFactory factory) {
        try {
            factory.setAddress(InetAddress.getByAddress(new byte[]{127, 0, 0, 1}));
        } catch (UnknownHostException impossible) {
            throw new IllegalStateException("could not configure the local MiniDock API address", impossible);
        }
    }
}
