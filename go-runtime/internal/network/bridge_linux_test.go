//go:build linux

package network

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type recordingRunner struct {
	commands     []string
	failOn       string
	bridgeExists bool
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	command := strings.Join(append([]string{name}, args...), " ")
	r.commands = append(r.commands, command)
	if command == "ip link show dev minidock0" && !r.bridgeExists {
		return errors.New("bridge does not exist")
	}
	if r.failOn != "" && strings.Contains(command, r.failOn) {
		return errors.New("injected command failure")
	}
	if command == "ip link add name minidock0 type bridge" {
		r.bridgeExists = true
	}
	return nil
}

func TestBridgeManagerConfigureAndCleanup(t *testing.T) {
	runner := new(recordingRunner)
	manager := NewBridgeManagerWithRunner(runner)
	cleanup, err := manager.Configure(context.Background(), "container-one", 4321)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.commands, "\n")
	for _, expected := range []string{
		"ip link add name minidock0 type bridge",
		"ip address replace 10.0.0.1/24 dev minidock0",
		"ip link add name mdh",
		"ip link set dev mdh",
		"nsenter -t 4321 -n -- ip link set dev mdp",
		"nsenter -t 4321 -n -- ip address add 10.0.0.2/24 dev eth0",
		"nsenter -t 4321 -n -- ip route replace default via 10.0.0.1 dev eth0",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("network setup did not run %q; commands:\n%s", expected, joined)
		}
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runner.commands[len(runner.commands)-1], "ip link del dev mdh") {
		t.Fatalf("cleanup did not delete the host veth: %s", runner.commands[len(runner.commands)-1])
	}
	if _, exists := manager.leases["container-one"]; exists {
		t.Fatal("cleanup retained the container IP lease")
	}
}

func TestBridgeManagerUsesDistinctAddresses(t *testing.T) {
	manager := NewBridgeManagerWithRunner(new(recordingRunner))
	firstCleanup, err := manager.Configure(context.Background(), "first", 101)
	if err != nil {
		t.Fatal(err)
	}
	secondCleanup, err := manager.Configure(context.Background(), "second", 102)
	if err != nil {
		t.Fatal(err)
	}
	if manager.leases["first"] == manager.leases["second"] {
		t.Fatalf("containers received duplicate addresses: %s", manager.leases["first"])
	}
	if err := firstCleanup(); err != nil {
		t.Fatal(err)
	}
	if err := secondCleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeManagerRollsBackCreatedVeth(t *testing.T) {
	runner := &recordingRunner{failOn: "master minidock0"}
	manager := NewBridgeManagerWithRunner(runner)
	if _, err := manager.Configure(context.Background(), "rollback", 55); err == nil {
		t.Fatal("expected injected setup failure")
	}
	if !strings.Contains(runner.commands[len(runner.commands)-1], "ip link del dev mdh") {
		t.Fatalf("failed setup did not remove the created veth: %s", runner.commands[len(runner.commands)-1])
	}
	if _, exists := manager.leases["rollback"]; exists {
		t.Fatal("failed setup retained its IP lease after successful rollback")
	}
}
