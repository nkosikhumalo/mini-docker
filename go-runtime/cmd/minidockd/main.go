// minidockd serves container launch operations over a local gRPC socket.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"mini-docker/go-runtime/internal/cgroups"
	"mini-docker/go-runtime/internal/container"
	"mini-docker/go-runtime/internal/isolation"
	"mini-docker/go-runtime/internal/network"
	"mini-docker/go-runtime/internal/transport"
)

// logHostPreflight reports host capabilities before accepting requests. Some
// requirements are feature-specific, so missing optional networking or cgroup
// support should not prevent no-network, no-limits containers from running.
func logHostPreflight() {
	if runtime.GOOS != "linux" {
		log.Fatalf("MiniDock requires Linux; current operating system is %s", runtime.GOOS)
	}
	if os.Geteuid() != 0 {
		log.Printf("host preflight: daemon is not root; user namespaces may allow basic containers, but cgroup limits and bridge networking may require elevated host permissions")
	}
	controllers, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers")
	if err != nil {
		log.Printf("host preflight: cgroups v2 not detected at /sys/fs/cgroup/cgroup.controllers; CPU/memory limits will be unavailable: %v", err)
	} else {
		available := " " + strings.TrimSpace(string(controllers)) + " "
		for _, controller := range []string{"cpu", "memory"} {
			if !strings.Contains(available, " "+controller+" ") {
				log.Printf("host preflight: cgroups v2 controller %q is unavailable; requests using that limit will fail", controller)
			}
		}
	}
	for _, name := range []string{"ip", "nsenter", "iptables"} {
		if _, err := exec.LookPath(name); err != nil {
			log.Printf("host preflight: %q is not installed; container networking is unavailable (Debian/Ubuntu packages: iproute2 iptables; nsenter is provided by util-linux)", name)
		}
	}
}

func main() {
	if isolation.IsChild() {
		status, err := isolation.RunChild()
		if err != nil {
			log.Printf("namespace child: %v", err)
		}
		os.Exit(status)
	}
	logHostPreflight()
	cgroupRoot := os.Getenv("MINIDOCK_CGROUP_ROOT")
	if cgroupRoot == "" {
		cgroupRoot = "/sys/fs/cgroup/minidock"
	}
	if err := cgroups.RemoveEmptyGroups(cgroupRoot); err != nil {
		log.Printf("startup cgroup recovery: %v", err)
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
	if err := os.Chmod(socket, 0660); err != nil {
		log.Fatalf("restrict runtime socket permissions: %v", err)
	}

	server := grpc.NewServer()
	networkManager := network.NewBridgeManager()
	logRoot := os.Getenv("MINIDOCK_CONTAINERS_DIR")
	if logRoot == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			log.Fatalf("resolve home directory for container logs: %v", homeErr)
		}
		logRoot = filepath.Join(home, ".local", "share", "minidock", "containers")
	}
	defer func() {
		if err := networkManager.Close(); err != nil {
			log.Printf("network cleanup: %v", err)
		}
	}()
	shutdownRuntime := transport.RegisterManagedRuntime(server, func(ctx context.Context, cfg container.Config, started func(container.StartedProcess)) error {
		return container.RunStarted(ctx, cfg, isolation.ChildStarter{Network: networkManager, LogRoot: logRoot}, started)
	})
	stopped := make(chan os.Signal, 1)
	shutdownDone := make(chan struct{})
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stopped
		shutdownRuntime()
		if err := networkManager.Close(); err != nil {
			log.Printf("network cleanup: %v", err)
		}
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
