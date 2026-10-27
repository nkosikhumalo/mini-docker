#!/usr/bin/env python3
"""Small localhost TCP forwarder used by run.sh for a web container."""

import argparse
import select
import socket
import socketserver
import threading


class ForwardHandler(socketserver.BaseRequestHandler):
    def handle(self):
        try:
            upstream = socket.create_connection((self.server.target_host, self.server.target_port), timeout=5)
        except OSError:
            return
        downstream = self.request
        downstream.settimeout(None)
        upstream.settimeout(None)
        peers = {downstream: upstream, upstream: downstream}
        try:
            while True:
                readable, _, exceptional = select.select(list(peers), [], list(peers))
                if exceptional:
                    return
                for source in readable:
                    data = source.recv(65536)
                    if not data:
                        return
                    peers[source].sendall(data)
        except (ConnectionError, OSError):
            pass
        finally:
            upstream.close()


class ForwardServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--listen", default="127.0.0.1")
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--target", required=True)
    parser.add_argument("--target-port", type=int, default=8080)
    args = parser.parse_args()
    server = ForwardServer((args.listen, args.port), ForwardHandler)
    server.target_host = args.target
    server.target_port = args.target_port
    print(f"Forwarding {args.listen}:{args.port} to {args.target}:{args.target_port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
