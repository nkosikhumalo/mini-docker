// minidockerd starts the local gRPC endpoint for the Go runtime.
// Container operations will be registered after the shared protocol is defined.
package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"mini-docker/go-runtime/internal/container"
	"mini-docker/go-runtime/internal/isolation"
)

func main() {
	if isolation.IsChild() {
		status, err := isolation.RunChild()
		if err != nil {
			log.Printf("namespace child: %v", err)
		}
		os.Exit(status)
	}
	if len(os.Args) == 2 && os.Args[1] == "namespace-check" {
		if err := namespaceCheck(); err != nil {
			log.Fatalf("namespace check: %v", err)
		}
		return
	}

	listener, err := net.Listen("tcp", "127.0.0.1:50051")
	if err != nil {
		log.Fatalf("listen for runtime gRPC: %v", err)
	}

	server := grpc.NewServer()
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stopped
		server.GracefulStop()
	}()

	log.Printf("Go runtime gRPC endpoint listening on %s", listener.Addr())
	if err := server.Serve(listener); err != nil {
		log.Fatalf("serve runtime gRPC: %v", err)
	}
}

// namespaceCheck is a safe smoke command: it starts only a shell that prints
// namespace handles, without mounting a rootfs or changing host networking.
func namespaceCheck() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := container.Config{
		ID:      "namespace-check",
		RootFS:  "/",
		Command: []string{"/bin/sh", "-c", "for ns in pid mnt net uts ipc; do printf '%s=' \"$ns\"; readlink /proc/self/ns/$ns; done"},
	}
	child, err := (isolation.ChildStarter{}).StartPaused(ctx, cfg)
	if err != nil {
		return err
	}
	defer child.Close()
	if err := child.Exec(ctx, cfg); err != nil {
		return err
	}
	return child.Wait()
}
