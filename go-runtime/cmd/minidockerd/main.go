// minidockerd starts the local gRPC endpoint for the Go runtime.
// Container operations will be registered after the shared protocol is defined.
package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
)

func main() {
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
