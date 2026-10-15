package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"pxe-platform/internal/network"
)

type Runner interface {
	Test(context.Context, string) error
	Restart(context.Context) error
	Stop(context.Context) error
}

type Status struct {
	Applied   bool   `json:"applied"`
	Mode      string `json:"mode,omitempty"`
	Interface string `json:"interface,omitempty"`
}

type Applier struct {
	mu         sync.Mutex
	configPath string
	statePath  string
	runner     Runner
	status     Status
}

// NewApplier restores the last successful config after an interrupted apply.
func NewApplier(configPath, statePath string, runner Runner) (*Applier, error) {
	if runner == nil || !filepath.IsAbs(configPath) || !filepath.IsAbs(statePath) {
		return nil, fmt.Errorf("runner and absolute paths are required")
	}
	a := &Applier{configPath: configPath, statePath: statePath, runner: runner}
	data, err := os.ReadFile(statePath)
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agent state: %w", err)
	}
	var previous network.Config
	if err := json.Unmarshal(data, &previous); err != nil {
		return nil, fmt.Errorf("parse agent state: %w", err)
	}
	config, err := network.RenderDnsmasq(previous)
	if err != nil {
		return nil, fmt.Errorf("invalid saved agent state: %w", err)
	}
	current, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read active dnsmasq config: %w", err)
	}
	if string(current) != config {
		if err := writeAtomic(configPath, []byte(config), 0o640); err != nil {
			return nil, fmt.Errorf("restore active dnsmasq config: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runner.Test(ctx, configPath); err != nil {
		return nil, fmt.Errorf("saved dnsmasq config test failed: %w", err)
	}
	if err := runner.Restart(ctx); err != nil {
		return nil, fmt.Errorf("restart saved PXE dnsmasq: %w", err)
	}
	a.status = Status{Applied: true, Mode: previous.Mode, Interface: previous.Interface}
	return a, nil
}

func (a *Applier) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// Apply validates, tests and restarts a dedicated PXE dnsmasq instance.
func (a *Applier) Apply(ctx context.Context, c network.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	content, err := network.RenderDnsmasq(c)
	if err != nil {
		return err
	}
	previous, readErr := os.ReadFile(a.configPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("read current dnsmasq config: %w", readErr)
	}
	previousExists := readErr == nil
	candidate, err := writeCandidate(a.configPath, []byte(content), 0o640)
	if err != nil {
		return err
	}
	defer os.Remove(candidate)
	if err := a.runner.Test(ctx, candidate); err != nil {
		return fmt.Errorf("dnsmasq config test failed: %w", err)
	}
	if err := os.Rename(candidate, a.configPath); err != nil {
		return fmt.Errorf("activate dnsmasq config: %w", err)
	}
	if err := a.runner.Restart(ctx); err != nil {
		return errors.Join(fmt.Errorf("restart PXE dnsmasq: %w", err), a.rollback(ctx, previous, previousExists))
	}
	state, err := json.Marshal(c)
	if err != nil {
		return errors.Join(err, a.rollback(ctx, previous, previousExists))
	}
	if err := writeAtomic(a.statePath, state, 0o600); err != nil {
		return errors.Join(fmt.Errorf("save agent state: %w", err), a.rollback(ctx, previous, previousExists))
	}
	a.status = Status{Applied: true, Mode: c.Mode, Interface: c.Interface}
	return nil
}

func (a *Applier) rollback(ctx context.Context, previous []byte, exists bool) error {
	if !exists {
		if err := os.Remove(a.configPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove failed first config: %w", err)
		}
		return a.runner.Stop(ctx)
	}
	if err := writeAtomic(a.configPath, previous, 0o640); err != nil {
		return fmt.Errorf("restore previous config: %w", err)
	}
	return a.runner.Restart(ctx)
}

func writeAtomic(path string, content []byte, mode os.FileMode) error {
	tmp, err := writeCandidate(path, content, mode)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Rename(tmp, path)
}

func writeCandidate(path string, content []byte, mode os.FileMode) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".pxe-candidate-*")
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := file.Chmod(mode); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	if _, err := file.Write(content); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	if err := file.Sync(); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}
