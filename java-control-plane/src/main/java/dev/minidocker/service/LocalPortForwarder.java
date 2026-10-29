package dev.minidocker.service;

import java.io.IOException;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.SocketException;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;

/** Local-only TCP forwarding from a loopback host port to a MiniDock bridge IP. */
final class LocalPortForwarder {
    private LocalPortForwarder() {}

    static Forward open(String targetIp, int targetPort, Integer preferredHostPort) throws IOException {
        ServerSocket listener = new ServerSocket();
        listener.setReuseAddress(false);
        listener.bind(new InetSocketAddress(InetAddress.getByName("127.0.0.1"),
                preferredHostPort == null ? 0 : preferredHostPort));
        Forward forward = new Forward(listener, targetIp, targetPort);
        Thread acceptor = new Thread(forward::accept, "minidock-port-" + listener.getLocalPort());
        acceptor.setDaemon(true);
        acceptor.start();
        return forward;
    }

    static final class Forward implements AutoCloseable {
        private final ServerSocket listener;
        private final String targetIp;
        private final int targetPort;
        private final Set<Socket> clients = ConcurrentHashMap.newKeySet();

        private Forward(ServerSocket listener, String targetIp, int targetPort) {
            this.listener = listener;
            this.targetIp = targetIp;
            this.targetPort = targetPort;
        }

        int hostPort() { return listener.getLocalPort(); }

        private void accept() {
            while (!listener.isClosed()) {
                try {
                    Socket client = listener.accept();
                    clients.add(client);
                    Thread.ofVirtual().start(() -> proxy(client));
                } catch (SocketException closed) {
                    if (!listener.isClosed()) System.err.println("minidock: port forward accept failed: " + closed);
                    return;
                } catch (IOException failure) {
                    System.err.println("minidock: port forward accept failed: " + failure);
                }
            }
        }

        private void proxy(Socket client) {
            Socket upstream = new Socket();
            try (client; upstream) {
                upstream.connect(new InetSocketAddress(targetIp, targetPort), 2000);
                clients.add(upstream);
                Thread inbound = Thread.ofVirtual().start(() -> copy(client, upstream));
                Thread outbound = Thread.ofVirtual().start(() -> copy(upstream, client));
                inbound.join();
                outbound.join();
            } catch (IOException ignored) {
                // The app may not be listening yet or may have closed the connection.
            } catch (InterruptedException interrupted) {
                Thread.currentThread().interrupt();
            } finally {
                clients.remove(client);
                clients.remove(upstream);
            }
        }

        private static void copy(Socket source, Socket destination) {
            try {
                source.getInputStream().transferTo(destination.getOutputStream());
                destination.shutdownOutput();
            } catch (IOException ignored) {
                try { destination.close(); } catch (IOException ignoredClose) {}
            }
        }

        @Override
        public void close() {
            try { listener.close(); } catch (IOException ignored) {}
            for (Socket socket : clients) {
                try { socket.close(); } catch (IOException ignored) {}
            }
            clients.clear();
        }
    }
}
