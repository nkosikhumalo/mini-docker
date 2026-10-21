//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"sync"
)

const (
	defaultBridge  = "minidock0"
	defaultGateway = "10.0.0.1"
	defaultPrefix  = 24
	containerIf    = "eth0"
)

// CommandRunner runs a host networking command. It can be replaced by a fake
// runner by callers that need to exercise network orchestration without CAP_NET_ADMIN.
type CommandRunner interface {
	Run(context.Context, string, ...string) error
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, name string, args ...string) error {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, output)
	}
	return nil
}

// BridgeManager creates a host bridge and veth pair for each opted-in
// container. It requires iproute2, util-linux nsenter, and host CAP_NET_ADMIN.
type BridgeManager struct {
	mu       sync.Mutex
	runner   CommandRunner
	bridge   string
	gateway  net.IP
	leases   map[string]string // container ID -> IPv4 address
	usedIPv4 map[string]bool
	nextHost int
}

func NewBridgeManager() *BridgeManager {
	return NewBridgeManagerWithRunner(commandRunner{})
}

func NewBridgeManagerWithRunner(runner CommandRunner) *BridgeManager {
	if runner == nil {
		runner = commandRunner{}
	}
	return &BridgeManager{
		runner:   runner,
		bridge:   defaultBridge,
		gateway:  net.ParseIP(defaultGateway).To4(),
		leases:   make(map[string]string),
		usedIPv4: map[string]bool{defaultGateway: true},
		nextHost: 2,
	}
}

// Configure ensures the bridge exists, attaches a veth to the isolated
// network namespace identified by pid, and returns cleanup for the veth/IP lease.
func (m *BridgeManager) Configure(ctx context.Context, id string, pid int) (func() error, error) {
	return m.ConfigureWithIP(ctx, id, pid, "")
}

// ConfigureWithIP reserves the requested address when supplied. An empty
// address asks the manager to allocate one from its default subnet.
func (m *BridgeManager) ConfigureWithIP(ctx context.Context, id string, pid int, requestedIP string) (func() error, error) {
	if id == "" || pid <= 0 {
		return nil, errors.New("network setup requires a container ID and positive pid")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.leases[id]; exists {
		return nil, fmt.Errorf("container %q already has a network lease", id)
	}
	var ipv4 string
	var err error
	if requestedIP == "" {
		ipv4, err = m.allocateIP()
	} else {
		ipv4, err = m.reserveIP(requestedIP)
	}
	if err != nil {
		return nil, err
	}
	m.leases[id] = ipv4
	rollback := func() {
		delete(m.leases, id)
		delete(m.usedIPv4, ipv4)
	}
	hostIf, peerIf := interfaceNames(id)
	vethCreated := false
	removeVeth := func() error {
		return m.runner.Run(context.Background(), "ip", "link", "del", "dev", hostIf)
	}
	fail := func(err error) (func() error, error) {
		if !vethCreated {
			rollback()
			return nil, err
		}
		if cleanupErr := removeVeth(); cleanupErr == nil {
			rollback()
		} else {
			err = errors.Join(err, fmt.Errorf("rollback veth setup: %w", cleanupErr))
		}
		return nil, err
	}

	if err := m.ensureBridge(ctx); err != nil {
		rollback()
		return nil, err
	}
	if err := m.runner.Run(ctx, "ip", "link", "add", "name", hostIf, "type", "veth", "peer", "name", peerIf); err != nil {
		return fail(fmt.Errorf("create veth pair: %w", err))
	}
	vethCreated = true
	for _, args := range [][]string{
		{"link", "set", "dev", hostIf, "master", m.bridge},
		{"link", "set", "dev", hostIf, "up"},
		{"link", "set", "dev", peerIf, "netns", strconv.Itoa(pid)},
	} {
		if err := m.runner.Run(ctx, "ip", args...); err != nil {
			return fail(fmt.Errorf("attach host veth: %w", err))
		}
	}

	namespaceCommand := func(args ...string) error {
		nsArgs := append([]string{"-t", strconv.Itoa(pid), "-n", "--", "ip"}, args...)
		return m.runner.Run(ctx, "nsenter", nsArgs...)
	}
	address := fmt.Sprintf("%s/%d", net.ParseIP(ipv4).To4().String(), defaultPrefix)
	for _, args := range [][]string{
		{"link", "set", "dev", peerIf, "name", containerIf},
		{"address", "add", address, "dev", containerIf},
		{"link", "set", "dev", containerIf, "up"},
		{"route", "replace", "default", "via", m.gateway.String(), "dev", containerIf},
	} {
		if err := namespaceCommand(args...); err != nil {
			return fail(fmt.Errorf("configure container network: %w", err))
		}
	}

	return func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		err := removeVeth()
		if err != nil {
			return fmt.Errorf("remove veth pair for %q: %w", id, err)
		}
		rollback()
		return nil
	}, nil
}

func (m *BridgeManager) ensureBridge(ctx context.Context) error {
	if err := m.runner.Run(ctx, "ip", "link", "show", "dev", m.bridge); err != nil {
		if err := m.runner.Run(ctx, "ip", "link", "add", "name", m.bridge, "type", "bridge"); err != nil {
			return fmt.Errorf("create bridge %s: %w", m.bridge, err)
		}
	}
	address := fmt.Sprintf("%s/%d", m.gateway.String(), defaultPrefix)
	if err := m.runner.Run(ctx, "ip", "address", "replace", address, "dev", m.bridge); err != nil {
		return fmt.Errorf("assign bridge address: %w", err)
	}
	if err := m.runner.Run(ctx, "ip", "link", "set", "dev", m.bridge, "up"); err != nil {
		return fmt.Errorf("bring bridge up: %w", err)
	}
	return nil
}

func (m *BridgeManager) allocateIP() (string, error) {
	for checked := 0; checked < 253; checked++ {
		octet := m.nextHost
		m.nextHost++
		if m.nextHost > 254 {
			m.nextHost = 2
		}
		candidate := fmt.Sprintf("10.0.0.%d", octet)
		if !m.usedIPv4[candidate] {
			m.usedIPv4[candidate] = true
			return candidate, nil
		}
	}
	return "", errors.New("default bridge subnet has no free container addresses")
}

func (m *BridgeManager) reserveIP(address string) (string, error) {
	ip := net.ParseIP(address).To4()
	if ip == nil || ip[0] != 10 || ip[1] != 0 || ip[2] != 0 || ip[3] < 2 || ip[3] > 254 {
		return "", fmt.Errorf("requested IP %q is outside the MiniDock bridge subnet", address)
	}
	canonical := ip.String()
	if m.usedIPv4[canonical] {
		return "", fmt.Errorf("requested IP %s is already allocated", canonical)
	}
	m.usedIPv4[canonical] = true
	return canonical, nil
}

func interfaceNames(id string) (string, string) {
	var hash uint32 = 2166136261
	for i := 0; i < len(id); i++ {
		hash = (hash ^ uint32(id[i])) * 16777619
	}
	return fmt.Sprintf("mdh%x", hash&0xfffffff), fmt.Sprintf("mdp%x", hash&0xfffffff)
}
