//go:build linux

package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const ownershipStateVersion = 1

type savedRule struct {
	Table string   `json:"table"`
	Chain string   `json:"chain"`
	Args  []string `json:"args"`
}

type ownershipState struct {
	Version           int         `json:"version"`
	BridgeOwned       bool        `json:"bridge_owned"`
	Veths             []string    `json:"veths,omitempty"`
	Rules             []savedRule `json:"rules,omitempty"`
	ForwardingRestore string      `json:"forwarding_restore,omitempty"`
}

// RecoverStale removes only resources recorded by the previous MiniDock
// daemon. Cleanup operations are idempotent so an interrupted recovery can be
// retried on the next start.
func (m *BridgeManager) RecoverStale(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.statePath == "" {
		return nil
	}
	if !filepath.IsAbs(m.statePath) {
		return errors.New("network ownership state path must be absolute")
	}
	if err := m.loadOwnershipLocked(); err != nil {
		return err
	}
	if len(m.veths) == 0 && len(m.natRules) == 0 && !m.ownedBridge && m.forwardingRestore == "" {
		return m.removeOwnershipStateLocked()
	}
	var result error
	for hostIf := range m.veths {
		if err := m.deleteVeth(ctx, hostIf); err != nil {
			result = errors.Join(result, err)
			continue
		}
		delete(m.veths, hostIf)
		result = errors.Join(result, m.saveOwnershipLocked())
	}
	for i := len(m.natRules) - 1; i >= 0; i-- {
		if err := m.deleteRule(ctx, m.natRules[i]); err != nil {
			result = errors.Join(result, err)
			continue
		}
		m.natRules = append(m.natRules[:i], m.natRules[i+1:]...)
		result = errors.Join(result, m.saveOwnershipLocked())
	}
	if m.forwardingRestore != "" {
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
	if m.ownedBridge && len(m.veths) == 0 {
		if err := m.runner.Run(ctx, "ip", "link", "del", "dev", m.bridge); err != nil && !isMissingLink(err) {
			result = errors.Join(result, fmt.Errorf("remove stale MiniDock bridge: %w", err))
		} else {
			m.ownedBridge = false
			result = errors.Join(result, m.saveOwnershipLocked())
		}
	} else if m.ownedBridge && len(m.veths) != 0 {
		result = errors.Join(result, errors.New("cannot remove stale MiniDock bridge while recorded veth cleanup is incomplete"))
	}
	if result == nil {
		result = m.removeOwnershipStateLocked()
	}
	return result
}

func (m *BridgeManager) saveOwnershipLocked() error {
	if m.statePath == "" {
		return nil
	}
	state := ownershipState{Version: ownershipStateVersion, BridgeOwned: m.ownedBridge,
		ForwardingRestore: m.forwardingRestore}
	for hostIf := range m.veths {
		state.Veths = append(state.Veths, hostIf)
	}
	for _, rule := range m.natRules {
		state.Rules = append(state.Rules, savedRule{Table: rule.table, Chain: rule.chain, Args: rule.args})
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode network ownership state: %w", err)
	}
	parent := filepath.Dir(m.statePath)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("create network state directory: %w", err)
	}
	if err := checkOwnershipDirectory(parent); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(parent, ".minidock-network-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary network state: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("restrict network state permissions: %w", err)
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write network ownership state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync network ownership state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close network ownership state: %w", err)
	}
	if err := os.Rename(temporaryName, m.statePath); err != nil {
		return fmt.Errorf("save network ownership state: %w", err)
	}
	return nil
}

func (m *BridgeManager) loadOwnershipLocked() error {
	info, err := os.Lstat(m.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect network ownership state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("network ownership state must be a regular non-symlink file")
	}
	if err := checkRootOwned(info); err != nil {
		return fmt.Errorf("network ownership state is not trusted: %w", err)
	}
	if info.Mode().Perm()&0022 != 0 {
		return errors.New("network ownership state must not be group or world writable")
	}
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return fmt.Errorf("read network ownership state: %w", err)
	}
	if len(data) > 1<<20 {
		return errors.New("network ownership state exceeds 1 MiB")
	}
	var state ownershipState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse network ownership state: %w", err)
	}
	if state.Version != ownershipStateVersion {
		return fmt.Errorf("unsupported network ownership state version %d", state.Version)
	}
	if state.ForwardingRestore != "" && state.ForwardingRestore != "0" && state.ForwardingRestore != "1" {
		return errors.New("network ownership state has an invalid forwarding value")
	}
	for _, hostIf := range state.Veths {
		if len(hostIf) < 4 || len(hostIf) > 15 || !strings.HasPrefix(hostIf, "mdh") {
			return fmt.Errorf("network ownership state has an invalid veth name %q", hostIf)
		}
		m.veths[hostIf] = true
	}
	for _, saved := range state.Rules {
		if (saved.Table != "" && saved.Table != "nat") || saved.Chain == "" || len(saved.Args) == 0 {
			return errors.New("network ownership state has an invalid firewall rule")
		}
		m.natRules = append(m.natRules, firewallRule{table: saved.Table, chain: saved.Chain, args: saved.Args})
	}
	m.ownedBridge = state.BridgeOwned
	m.forwardingRestore = state.ForwardingRestore
	return nil
}

func (m *BridgeManager) removeOwnershipStateLocked() error {
	if m.statePath == "" {
		return nil
	}
	if err := os.Remove(m.statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove network ownership state: %w", err)
	}
	return nil
}

func (m *BridgeManager) deleteVeth(ctx context.Context, hostIf string) error {
	if err := m.runner.Run(ctx, "ip", "link", "del", "dev", hostIf); err != nil && !isMissingLink(err) {
		return fmt.Errorf("remove recorded MiniDock veth %s: %w", hostIf, err)
	}
	return nil
}

func isMissingLink(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "cannot find device") || strings.Contains(message, "does not exist") || strings.Contains(message, "no such device")
}

func defaultOwnershipStatePath() string {
	if configured := os.Getenv("MINIDOCK_NETWORK_STATE_FILE"); configured != "" {
		return filepath.Clean(configured)
	}
	return "/run/minidock/network-state.json"
}

func checkOwnershipDirectory(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve network state directory: %w", err)
	}
	if filepath.Clean(resolved) != filepath.Clean(path) {
		return errors.New("network state directory must not use symbolic links")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect network state directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("network state parent must be a directory")
	}
	if err := checkRootOwned(info); err != nil {
		return fmt.Errorf("network state directory is not trusted: %w", err)
	}
	if info.Mode().Perm()&0022 != 0 {
		return errors.New("network state directory must not be group or world writable")
	}
	return nil
}

func checkRootOwned(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return errors.New("must be owned by root")
	}
	return nil
}
