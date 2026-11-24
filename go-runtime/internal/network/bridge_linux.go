//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

type Forwarder interface{ Enable() (func() error, error) }

type procForwarder struct{ path string }

func (p procForwarder) Enable() (func() error, error) {
	previous, err := p.Read()
	if err != nil {
		return nil, err
	}
	if previous == "1" {
		return func() error { return nil }, nil
	}
	if previous != "0" {
		return nil, fmt.Errorf("unexpected IPv4 forwarding value %q", previous)
	}
	if err := p.Set("1"); err != nil {
		return nil, fmt.Errorf("enable IPv4 forwarding: %w", err)
	}
	return func() error { return p.Restore(previous) }, nil
}

func (p procForwarder) Read() (string, error) {
	value, err := os.ReadFile(p.path)
	if err != nil {
		return "", fmt.Errorf("read IPv4 forwarding setting: %w", err)
	}
	return strings.TrimSpace(string(value)), nil
}

func (p procForwarder) Set(value string) error {
	if value != "0" && value != "1" {
		return fmt.Errorf("invalid IPv4 forwarding value %q", value)
	}
	if err := os.WriteFile(p.path, []byte(value), 0644); err != nil {
		return fmt.Errorf("write IPv4 forwarding setting: %w", err)
	}
	return nil
}

func (p procForwarder) Restore(previous string) error {
	current, err := p.Read()
	if err != nil {
		return fmt.Errorf("read IPv4 forwarding setting during restore: %w", err)
	}
	if current != "1" {
		return nil
	}
	if err := p.Set(previous); err != nil {
		return fmt.Errorf("restore IPv4 forwarding setting: %w", err)
	}
	return nil
}

type firewallRule struct {
	table, chain string
	args         []string
}

// BridgeManager creates a host bridge and veth pair for each opted-in
// container. It requires iproute2, util-linux nsenter, and host CAP_NET_ADMIN.
type BridgeManager struct {
	mu                sync.Mutex
	runner            CommandRunner
	bridge            string
	gateway           net.IP
	leases            map[string]string // container ID -> IPv4 address
	usedIPv4          map[string]bool
	nextHost          int
	forwarder         Forwarder
	forwardingCleanup func() error
	natRules          []firewallRule
	natReady          bool
	bridgeReady       bool
	ownedBridge       bool
	statePath         string
	veths             map[string]bool
	forwardingRestore string
}

func NewBridgeManager() *BridgeManager {
	manager := NewBridgeManagerWithRunner(commandRunner{})
	manager.forwarder = procForwarder{path: "/proc/sys/net/ipv4/ip_forward"}
	manager.statePath = defaultOwnershipStatePath()
	return manager
}

func NewBridgeManagerWithRunner(runner CommandRunner) *BridgeManager {
	if runner == nil {
		runner = commandRunner{}
	}
	return &BridgeManager{
		runner:    runner,
		bridge:    defaultBridge,
		gateway:   net.ParseIP(defaultGateway).To4(),
		leases:    make(map[string]string),
		usedIPv4:  map[string]bool{defaultGateway: true},
		nextHost:  2,
		veths:     make(map[string]bool),
		forwarder: ForwarderFunc(func() (func() error, error) { return func() error { return nil }, nil }),
	}
}

type ForwarderFunc func() (func() error, error)

func (f ForwarderFunc) Enable() (func() error, error) { return f() }

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
		err := m.runner.Run(context.Background(), "ip", "link", "del", "dev", hostIf)
		if isMissingLink(err) {
			return nil
		}
		return err
	}
	fail := func(err error) (func() error, error) {
		if !vethCreated && !m.veths[hostIf] {
			rollback()
			return nil, err
		}
		if cleanupErr := removeVeth(); cleanupErr == nil {
			if vethCreated || m.veths[hostIf] {
				delete(m.veths, hostIf)
				if stateErr := m.saveOwnershipLocked(); stateErr != nil {
					err = errors.Join(err, fmt.Errorf("save network cleanup state: %w", stateErr))
				}
			}
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
	// Write the intended interface name before creating it. On restart, an
	// absent name is harmless and a created pair can be removed safely.
	m.veths[hostIf] = true
	if err := m.saveOwnershipLocked(); err != nil {
		delete(m.veths, hostIf)
		rollback()
		return nil, fmt.Errorf("record veth ownership: %w", err)
	}
	if err := m.runner.Run(ctx, "ip", "link", "add", "name", hostIf, "type", "veth", "peer", "name", peerIf); err != nil {
		delete(m.veths, hostIf)
		_ = m.saveOwnershipLocked()
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
		delete(m.veths, hostIf)
		stateErr := m.saveOwnershipLocked()
		rollback()
		return stateErr
	}, nil
}

func (m *BridgeManager) ensureBridge(ctx context.Context) error {
	if !m.bridgeReady {
		if !m.ownedBridge {
			if err := m.runner.Run(ctx, "ip", "link", "show", "dev", m.bridge); err == nil {
				return fmt.Errorf("bridge %s already exists; refusing to change a host interface MiniDock does not own", m.bridge)
			}
			m.ownedBridge = true
			if err := m.saveOwnershipLocked(); err != nil {
				m.ownedBridge = false
				return fmt.Errorf("record bridge ownership: %w", err)
			}
			if err := m.runner.Run(ctx, "ip", "link", "add", "name", m.bridge, "type", "bridge"); err != nil {
				m.ownedBridge = false
				_ = m.saveOwnershipLocked()
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
		m.bridgeReady = true
	}
	if !m.natReady {
		if err := m.enableNAT(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (m *BridgeManager) enableNAT(ctx context.Context) error {
	cleanupForwarding, err := m.enableForwarding()
	if err != nil {
		return err
	}
	if cleanupForwarding == nil {
		cleanupForwarding = func() error { return nil }
	}
	rules := []firewallRule{
		{table: "nat", chain: "POSTROUTING", args: []string{"-s", "10.0.0.0/24", "!", "-o", m.bridge, "-j", "MASQUERADE", "-m", "comment", "--comment", "minidock-managed"}},
		{chain: "FORWARD", args: []string{"-i", m.bridge, "-j", "ACCEPT", "-m", "comment", "--comment", "minidock-managed"}},
		{chain: "FORWARD", args: []string{"-o", m.bridge, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT", "-m", "comment", "--comment", "minidock-managed"}},
	}
	added := make([]firewallRule, 0, len(rules))
	for _, rule := range rules {
		check := append([]string{}, rule.args...)
		check = append([]string{"-t", tableName(rule.table), "-C", rule.chain}, check...)
		if m.runner.Run(ctx, "iptables", check...) == nil {
			// This rule predates this manager (or is shared with another
			// manager). It provides the required behavior, but MiniDock must
			// not claim ownership and remove it during cleanup.
			continue
		}
		m.natRules = append(m.natRules, rule)
		if err := m.saveOwnershipLocked(); err != nil {
			m.natRules = m.natRules[:len(m.natRules)-1]
			_ = cleanupForwarding()
			return fmt.Errorf("record firewall rule ownership: %w", err)
		}
		add := append([]string{}, rule.args...)
		add = append([]string{"-t", tableName(rule.table), "-A", rule.chain}, add...)
		if err := m.runner.Run(ctx, "iptables", add...); err != nil {
			result := fmt.Errorf("install outbound NAT/forwarding rule: %w", err)
			if removeErr := m.deleteRule(context.Background(), rule); removeErr == nil {
				m.forgetRuleLocked(rule)
			} else {
				result = errors.Join(result, removeErr)
			}
			for i := len(added) - 1; i >= 0; i-- {
				if removeErr := m.deleteRule(context.Background(), added[i]); removeErr == nil {
					m.forgetRuleLocked(added[i])
				} else {
					result = errors.Join(result, removeErr)
				}
			}
			if restoreErr := cleanupForwarding(); restoreErr == nil {
				m.forwardingRestore = ""
				result = errors.Join(result, m.saveOwnershipLocked())
			} else {
				result = errors.Join(result, fmt.Errorf("restore IPv4 forwarding after setup failure: %w", restoreErr))
			}
			return result
		}
		added = append(added, rule)
	}
	m.forwardingCleanup = cleanupForwarding
	m.natReady = true
	return nil
}

func (m *BridgeManager) forgetRuleLocked(target firewallRule) {
	for index, rule := range m.natRules {
		if rule.table != target.table || rule.chain != target.chain || len(rule.args) != len(target.args) {
			continue
		}
		match := true
		for i := range rule.args {
			if rule.args[i] != target.args[i] {
				match = false
				break
			}
		}
		if match {
			m.natRules = append(m.natRules[:index], m.natRules[index+1:]...)
			_ = m.saveOwnershipLocked()
			return
		}
	}
}

func (m *BridgeManager) enableForwarding() (func() error, error) {
	forwarder, ok := m.forwarder.(interface {
		Read() (string, error)
		Set(string) error
		Restore(string) error
	})
	if !ok {
		return m.forwarder.Enable()
	}
	previous, err := forwarder.Read()
	if err != nil {
		return nil, err
	}
	switch previous {
	case "1":
		return func() error { return nil }, nil
	case "0":
		m.forwardingRestore = previous
		if err := m.saveOwnershipLocked(); err != nil {
			m.forwardingRestore = ""
			return nil, fmt.Errorf("record IPv4 forwarding state: %w", err)
		}
		if err := forwarder.Set("1"); err != nil {
			m.forwardingRestore = ""
			_ = m.saveOwnershipLocked()
			return nil, fmt.Errorf("enable IPv4 forwarding: %w", err)
		}
		return func() error { return forwarder.Restore(previous) }, nil
	default:
		return nil, fmt.Errorf("unexpected IPv4 forwarding value %q", previous)
	}
}

func tableName(table string) string {
	if table == "" {
		return "filter"
	}
	return table
}

func (m *BridgeManager) deleteRule(ctx context.Context, rule firewallRule) error {
	args := append([]string{}, rule.args...)
	args = append([]string{"-t", tableName(rule.table), "-D", rule.chain}, args...)
	if err := m.runner.Run(ctx, "iptables", args...); err != nil {
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "bad rule") && !strings.Contains(message, "no chain/target/match") &&
			!strings.Contains(message, "does a matching rule exist") && !strings.Contains(message, "rule does not exist") {
			return fmt.Errorf("remove iptables rule: %w", err)
		}
	}
	return nil
}

// Close removes the firewall rules created by this manager and restores the
// prior forwarding setting. It should run after all container veths are gone.
func (m *BridgeManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result error
	for hostIf := range m.veths {
		if err := m.deleteVeth(context.Background(), hostIf); err != nil {
			result = errors.Join(result, err)
			continue
		}
		delete(m.veths, hostIf)
		result = errors.Join(result, m.saveOwnershipLocked())
	}
	for i := len(m.natRules) - 1; i >= 0; i-- {
		if err := m.deleteRule(context.Background(), m.natRules[i]); err != nil {
			result = errors.Join(result, err)
			continue
		}
		m.natRules = append(m.natRules[:i], m.natRules[i+1:]...)
		result = errors.Join(result, m.saveOwnershipLocked())
	}
	if m.forwardingCleanup != nil {
		if err := m.forwardingCleanup(); err != nil {
			result = errors.Join(result, err)
		} else {
			m.forwardingCleanup = nil
			m.forwardingRestore = ""
			result = errors.Join(result, m.saveOwnershipLocked())
		}
	} else if m.forwardingRestore != "" {
		forwarder, ok := m.forwarder.(interface{ Restore(string) error })
		if !ok {
			result = errors.Join(result, errors.New("saved IPv4 forwarding state cannot be restored by this forwarder"))
		} else if err := forwarder.Restore(m.forwardingRestore); err != nil {
			result = errors.Join(result, err)
		} else {
			m.forwardingRestore = ""
			result = errors.Join(result, m.saveOwnershipLocked())
		}
	}
	if m.ownedBridge {
		if len(m.veths) != 0 {
			result = errors.Join(result, errors.New("cannot remove MiniDock bridge while recorded veth cleanup is incomplete"))
		} else if err := m.runner.Run(context.Background(), "ip", "link", "del", "dev", m.bridge); err != nil && !isMissingLink(err) {
			result = errors.Join(result, fmt.Errorf("remove bridge: %w", err))
		} else {
			m.ownedBridge = false
			result = errors.Join(result, m.saveOwnershipLocked())
		}
	}
	m.bridgeReady = false
	m.natReady = false
	if result == nil {
		result = m.removeOwnershipStateLocked()
	}
	return result
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
