// minidockerd serves container lifecycle operations over a local gRPC socket.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"mini-docker/go-runtime/internal/container"
	"mini-docker/go-runtime/internal/isolation"
	"mini-docker/go-runtime/internal/transport"
)

func main() {
	if isolation.IsChild() {
		status, err := isolation.RunChild()
		if err != nil {
			log.Printf("namespace child: %v", err)
		}
		os.Exit(status)
	}
	socket := os.Getenv("MINIDOCK_SOCKET")
	if socket == "" {
		socket = "/tmp/minidock-runtime.sock"
	}
	if !filepath.IsAbs(socket) {
		log.Fatalf("runtime socket path must be absolute: %s", socket)
	}
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			log.Fatalf("refusing to replace non-socket path %s", socket)
		}
		if connection, dialErr := net.DialTimeout("unix", socket, time.Second); dialErr == nil {
			_ = connection.Close()
			log.Fatalf("another runtime is already listening on %s", socket)
		}
		if err := os.Remove(socket); err != nil {
			log.Fatalf("remove stale runtime socket: %v", err)
		}
	} else if !os.IsNotExist(err) {
		log.Fatalf("inspect runtime socket path: %v", err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		log.Fatalf("listen for runtime gRPC socket: %v", err)
	}
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0600); err != nil {
		log.Fatalf("restrict runtime socket permissions: %v", err)
	}

	server := grpc.NewServer()
	transport.RegisterRuntime(server, func(ctx context.Context, cfg container.Config) error {
		return container.Run(ctx, cfg, isolation.ChildStarter{})
	})
	stopped := make(chan os.Signal, 1)
	shutdownDone := make(chan struct{})
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stopped
		server.Stop()
		close(shutdownDone)
	}()

	log.Printf("Low-level container runtime ready on unix://%s", socket)
	if err := server.Serve(listener); err != nil {
		if errors.Is(err, grpc.ErrServerStopped) {
			<-shutdownDone
			return
		}
		log.Fatalf("serve runtime gRPC: %v", err)
	}
}
