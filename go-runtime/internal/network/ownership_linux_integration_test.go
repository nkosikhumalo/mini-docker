//go:build linux && integration

package network

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	recoveryHelperEnv = "MINIDOCK_RECOVERY_HELPER"
	recoveryReadyEnv  = "MINIDOCK_RECOVERY_READY_FILE"
)

// TestRecoveryAfterForcedDaemonTermination validates persisted ownership and
// startup cleanup against real host networking after the owning process gets
// SIGKILL. Run only through the explicit e2e-recovery target on Linux.
func TestRecoveryAfterForcedDaemonTermination(t *testing.T) {
	if os.Getenv(recoveryHelperEnv) == "1" {
		runRecoveryResourceOwner(t)
		return
	}
	if os.Geteuid() != 0 {
		t.Fatal("forced-termination recovery integration requires root")
	}
	if err := (commandRunner{}).Run(context.Background(), "ip", "link", "show", "dev", defaultBridge); err == nil {
		t.Fatalf("refusing to test: %s already exists", defaultBridge)
	}
	if output, err := exec.Command("ip", "-4", "route", "show", "10.0.0.0/24").CombinedOutput(); err != nil {
		t.Fatalf("check host routes: %v: %s", err, output)
	} else if strings.TrimSpace(string(output)) != "" {
		t.Fatalf("refusing to test: host already has a route for 10.0.0.0/24: %s", output)
	}
	forwardingBefore, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		t.Fatalf("read initial IPv4 forwarding value: %v", err)
	}

	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, "network-state.json")
	readyPath := filepath.Join(stateDir, "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryAfterForcedDaemonTermination$")
	cmd.Env = append(os.Environ(), "MINIDOCK_NETWORK_STATE_FILE="+statePath,
		recoveryHelperEnv+"=1", recoveryReadyEnv+"="+readyPath)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start resource-owning helper: %v", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			<-wait
			waited = true
		}
		manager := NewBridgeManager()
		manager.statePath = statePath
		if err := manager.RecoverStale(context.Background()); err != nil {
			t.Errorf("clean up integration resources: %v", err)
		}
	}()

	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		select {
		case err := <-wait:
			waited = true
			t.Fatalf("resource owner exited before setup completed: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for network setup")
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("send SIGKILL to resource owner: %v", err)
	}
	waitErr := <-wait
	waited = true
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		t.Fatalf("resource owner did not exit from SIGKILL: %v", waitErr)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("resource owner was not killed by SIGKILL: %v", exitErr)
	}

	var saved ownershipState
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read persisted ownership after SIGKILL: %v", err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("decode persisted ownership after SIGKILL: %v", err)
	}
	manager := NewBridgeManager()
	manager.statePath = statePath
	if err := manager.RecoverStale(context.Background()); err != nil {
		t.Fatalf("recover recorded host resources: %v", err)
	}

	for _, device := range append([]string{defaultBridge}, saved.Veths...) {
		if err := (commandRunner{}).Run(context.Background(), "ip", "link", "show", "dev", device); err == nil {
			t.Errorf("recovery left interface %s behind", device)
		}
	}
	for _, rule := range saved.Rules {
		args := append([]string{}, rule.Args...)
		args = append([]string{"-t", tableName(rule.Table), "-C", rule.Chain}, args...)
		if err := (commandRunner{}).Run(context.Background(), "iptables", args...); err == nil {
			t.Errorf("recovery left owned firewall rule in %s/%s: %v", tableName(rule.Table), rule.Chain, rule.Args)
		}
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ownership manifest remains after successful recovery: %v", err)
	}
	forwardingAfter, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		t.Fatalf("read IPv4 forwarding after recovery: %v", err)
	}
	if strings.TrimSpace(string(forwardingBefore)) != strings.TrimSpace(string(forwardingAfter)) {
		t.Errorf("IPv4 forwarding value changed during recovery: before=%q after=%q",
			strings.TrimSpace(string(forwardingBefore)), strings.TrimSpace(string(forwardingAfter)))
	}
}

func runRecoveryResourceOwner(t *testing.T) {
	t.Helper()
	manager := NewBridgeManager()
	manager.mu.Lock()
	err := manager.ensureBridge(context.Background())
	manager.mu.Unlock()
	if err != nil {
		t.Fatalf("create bridge and firewall state: %v", err)
	}
	hostIf, peerIf := interfaceNames("forced-recovery-e2e")
	manager.mu.Lock()
	manager.veths[hostIf] = true
	err = manager.saveOwnershipLocked()
	manager.mu.Unlock()
	if err != nil {
		t.Fatalf("record veth ownership: %v", err)
	}
	if err := manager.runner.Run(context.Background(), "ip", "link", "add", "name", hostIf,
		"type", "veth", "peer", "name", peerIf); err != nil {
		t.Fatalf("create veth pair: %v", err)
	}
	if err := manager.runner.Run(context.Background(), "ip", "link", "set", "dev", hostIf, "master", defaultBridge); err != nil {
		t.Fatalf("attach veth to MiniDock bridge: %v", err)
	}
	if err := manager.runner.Run(context.Background(), "ip", "link", "set", "dev", hostIf, "up"); err != nil {
		t.Fatalf("bring veth up: %v", err)
	}
	if err := os.WriteFile(os.Getenv(recoveryReadyEnv), []byte("ready\n"), 0600); err != nil {
		t.Fatalf("signal network setup complete: %v", err)
	}
	select {}
}
