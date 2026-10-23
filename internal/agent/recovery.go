package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RecoveryConfig controls automatic recovery of storage mounts and Windows ISO
// profiles. It is stored by Agent so that recovery works right after boot,
// before Control or PostgreSQL are available.
type RecoveryConfig struct {
	Enabled         bool `json:"enabled"`
	IntervalSeconds int  `json:"interval_seconds"`
}

// RecoveryStatus reports the configuration and the result of the last run.
type RecoveryStatus struct {
	RecoveryConfig
	LastRun   time.Time `json:"last_run"`
	Actions   []string  `json:"actions"`
	LastError string    `json:"last_error,omitempty"`
}

const (
	minRecoveryInterval     = 60
	maxRecoveryInterval     = 3600
	disabledRecoveryRecheck = 30 * time.Second
)

func defaultRecoveryConfig() RecoveryConfig {
	return RecoveryConfig{Enabled: true, IntervalSeconds: 60}
}

// StorageRecovery restarts failed SMB mount units and then failed Windows ISO
// profile units, for example when the NAS becomes reachable after the PXE
// server has already booted.
type StorageRecovery struct {
	SMB        *SMBManager
	Windows    *WindowsISOManager
	ConfigPath string

	runMu  sync.Mutex
	mu     sync.Mutex
	status RecoveryStatus
}

// Config returns the stored configuration, or the default when none is stored.
func (r *StorageRecovery) Config() RecoveryConfig {
	content, err := os.ReadFile(r.ConfigPath)
	if err != nil {
		return defaultRecoveryConfig()
	}
	var cfg RecoveryConfig
	if json.Unmarshal(content, &cfg) != nil || validateRecoveryConfig(cfg) != nil {
		return defaultRecoveryConfig()
	}
	return cfg
}

func validateRecoveryConfig(cfg RecoveryConfig) error {
	if cfg.IntervalSeconds < minRecoveryInterval || cfg.IntervalSeconds > maxRecoveryInterval {
		return fmt.Errorf("interval must be between %d and %d seconds", minRecoveryInterval, maxRecoveryInterval)
	}
	return nil
}

// SetConfig validates and stores the configuration.
func (r *StorageRecovery) SetConfig(cfg RecoveryConfig) error {
	if err := validateRecoveryConfig(cfg); err != nil {
		return err
	}
	content, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return writeAtomic(r.ConfigPath, content, 0600)
}

// Status returns the configuration and the result of the last run.
func (r *StorageRecovery) Status() RecoveryStatus {
	r.mu.Lock()
	status := r.status
	r.mu.Unlock()
	status.RecoveryConfig = r.Config()
	if status.Actions == nil {
		status.Actions = []string{}
	}
	return status
}

// RunOnce checks and recovers storage mounts first and profiles second.
// Profiles are not touched while a storage mount is still failing.
func (r *StorageRecovery) RunOnce(ctx context.Context) RecoveryStatus {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	actions := []string{}
	var problems []error
	if r.SMB != nil {
		// A share that is mounted but no longer readable (for example after
		// the NAS folder was recreated) stays "active" in systemd. Free it
		// from the ISO profiles that use it, then mount it again.
		if stale := r.SMB.StaleMounts(); len(stale) > 0 {
			if r.Windows != nil {
				var roots []string
				for _, mount := range stale {
					roots = append(roots, mount.Target)
				}
				done, err := r.Windows.StopProfilesOn(ctx, roots)
				actions = append(actions, done...)
				if err != nil {
					problems = append(problems, err)
				}
			}
			done, err := r.SMB.RemountStale(ctx, stale)
			actions = append(actions, done...)
			if err != nil {
				problems = append(problems, err)
			}
		}
		done, err := r.SMB.RecoverMounts(ctx)
		actions = append(actions, done...)
		if err != nil {
			problems = append(problems, err)
		}
	}
	if len(problems) == 0 && r.Windows != nil {
		done, err := r.Windows.RecoverProfiles(ctx)
		actions = append(actions, done...)
		if err != nil {
			problems = append(problems, err)
		}
	}
	status := RecoveryStatus{LastRun: time.Now(), Actions: actions}
	if len(problems) > 0 {
		status.LastError = errors.Join(problems...).Error()
	}
	for _, action := range actions {
		log.Printf("Storage recovery: %s", action)
	}
	if status.LastError != "" {
		log.Printf("Storage recovery: %s", status.LastError)
	}
	r.mu.Lock()
	r.status = status
	r.mu.Unlock()
	status.RecoveryConfig = r.Config()
	return status
}

// Run performs recovery on the configured schedule until ctx is done.
func (r *StorageRecovery) Run(ctx context.Context) {
	for {
		cfg := r.Config()
		wait := disabledRecoveryRecheck
		if cfg.Enabled {
			r.RunOnce(ctx)
			wait = time.Duration(cfg.IntervalSeconds) * time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// RecoverMounts restarts managed SMB mount units that are not active.
func (m *SMBManager) RecoverMounts(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Systemctl == nil {
		return nil, fmt.Errorf("SMB mount manager is not configured")
	}
	creds, err := filepath.Glob(filepath.Join(m.CredentialsDir, "*.cred"))
	if err != nil {
		return nil, err
	}
	sort.Strings(creds)
	var actions []string
	var problems []error
	for _, cred := range creds {
		key := strings.TrimSuffix(filepath.Base(cred), ".cred")
		if key == "" || strings.ContainsAny(key, "/\\") {
			continue
		}
		roots := []string{m.MountRoot}
		if m.UploadRoot != "" {
			roots = append(roots, m.UploadRoot)
		}
		for _, root := range roots {
			unit := smbUnitName(filepath.Join(root, key))
			if info, err := os.Lstat(filepath.Join(m.UnitDir, unit)); err != nil || !info.Mode().IsRegular() {
				continue
			}
			done, err := restartIfInactive(ctx, m.Systemctl, unit)
			if done {
				actions = append(actions, "restarted "+unit)
			}
			if err != nil {
				problems = append(problems, err)
			}
		}
	}
	return actions, errors.Join(problems...)
}

// probeTimeout bounds a readability check of a mounted share; a hung SMB
// server must not stall recovery.
const probeTimeout = 15 * time.Second

// probeDir reads one entry of a directory, giving up after probeTimeout.
func probeDir(path string) error {
	done := make(chan error, 1)
	go func() {
		f, err := os.Open(path)
		if err == nil {
			_, err = f.Readdirnames(1)
			if errors.Is(err, io.EOF) {
				err = nil
			}
			_ = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(probeTimeout):
		return fmt.Errorf("no answer in %s", probeTimeout)
	}
}

// staleMount is an SMB mount unit that is active but cannot be read.
type staleMount struct {
	Unit, Target string
	Err          error
}

// StaleMounts lists managed SMB mounts that systemd reports active but whose
// directory cannot be read, such as a "Stale file handle" after the NAS
// recreated the shared folder.
func (m *SMBManager) StaleMounts() []staleMount {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Systemctl == nil {
		return nil
	}
	probe := m.Probe
	if probe == nil {
		probe = probeDir
	}
	var stale []staleMount
	for _, target := range m.mountTargets() {
		unit := smbUnitName(target)
		if m.Systemctl.Run(context.Background(), "is-active", "--quiet", unit) != nil {
			continue // inactive mounts are restarted by RecoverMounts
		}
		if err := probe(target); err != nil {
			stale = append(stale, staleMount{Unit: unit, Target: target, Err: err})
		}
	}
	return stale
}

// RemountStale restarts the given mount units so the kernel connects again.
func (m *SMBManager) RemountStale(ctx context.Context, stale []staleMount) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var actions []string
	var problems []error
	for _, mount := range stale {
		if err := m.Systemctl.Run(ctx, "restart", mount.Unit); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", mount.Unit, err))
			continue
		}
		actions = append(actions, fmt.Sprintf("remounted %s (was unreadable: %v)", mount.Unit, mount.Err))
	}
	return actions, errors.Join(problems...)
}

// mountTargets lists the directories of the managed SMB mounts that have a
// unit file: the read-only mount and, when configured, the upload mount.
func (m *SMBManager) mountTargets() []string {
	creds, err := filepath.Glob(filepath.Join(m.CredentialsDir, "*.cred"))
	if err != nil {
		return nil
	}
	sort.Strings(creds)
	roots := []string{m.MountRoot}
	if m.UploadRoot != "" {
		roots = append(roots, m.UploadRoot)
	}
	var targets []string
	for _, cred := range creds {
		key := strings.TrimSuffix(filepath.Base(cred), ".cred")
		if key == "" || strings.ContainsAny(key, "/\\") {
			continue
		}
		for _, root := range roots {
			target := filepath.Join(root, key)
			if info, err := os.Lstat(filepath.Join(m.UnitDir, smbUnitName(target))); err == nil && info.Mode().IsRegular() {
				targets = append(targets, target)
			}
		}
	}
	return targets
}

// StopProfilesOn stops the active Windows ISO profile units whose ISO lives
// under one of roots, so their loop mounts release the share. RecoverProfiles
// starts them again once the share is back.
func (m *WindowsISOManager) StopProfilesOn(ctx context.Context, roots []string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Systemctl == nil {
		return nil, fmt.Errorf("systemd control is unavailable")
	}
	configs, err := filepath.Glob(filepath.Join(m.ConfigRoot, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(configs)
	var actions []string
	var problems []error
	for _, config := range configs {
		id, err := strconv.ParseInt(strings.TrimSuffix(filepath.Base(config), ".json"), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		content, err := os.ReadFile(config)
		if err != nil {
			continue
		}
		var req WindowsISORequest
		if json.Unmarshal(content, &req) != nil {
			continue
		}
		uses := false
		for _, root := range roots {
			clean := filepath.Clean(req.StorageRoot)
			uses = uses || clean == root || strings.HasPrefix(clean, root+string(filepath.Separator))
		}
		if !uses {
			continue
		}
		unit := "pxe-winiso@" + strconv.FormatInt(id, 10) + ".service"
		if m.Systemctl.Run(ctx, "is-active", "--quiet", unit) != nil {
			continue
		}
		if err := m.Systemctl.Run(ctx, "stop", unit); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", unit, err))
			continue
		}
		actions = append(actions, "stopped "+unit+" to release its share")
	}
	return actions, errors.Join(problems...)
}

// RecoverProfiles restarts prepared Windows ISO profile units that are not active.
func (m *WindowsISOManager) RecoverProfiles(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Systemctl == nil {
		return nil, fmt.Errorf("systemd control is unavailable")
	}
	configs, err := filepath.Glob(filepath.Join(m.ConfigRoot, "*.json"))
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, config := range configs {
		id, err := strconv.ParseInt(strings.TrimSuffix(filepath.Base(config), ".json"), 10, 64)
		if err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var actions []string
	var problems []error
	for _, id := range ids {
		unit := "pxe-winiso@" + strconv.FormatInt(id, 10) + ".service"
		done, err := restartIfInactive(ctx, m.Systemctl, unit)
		if done {
			actions = append(actions, "restarted "+unit)
		}
		if err != nil {
			problems = append(problems, err)
		}
	}
	return actions, errors.Join(problems...)
}

func restartIfInactive(ctx context.Context, systemctl SMBSystemctl, unit string) (bool, error) {
	if systemctl.Run(ctx, "is-active", "--quiet", unit) == nil {
		return false, nil
	}
	if err := systemctl.Run(ctx, "restart", unit); err != nil {
		return false, fmt.Errorf("%s: %w", unit, err)
	}
	return true, nil
}

// HandlerWithRecovery adds the storage recovery API to the Agent handler.
func HandlerWithRecovery(applier *Applier, manager *SMBManager, windows *WindowsISOManager, driverManager *DriverManager, recovery *StorageRecovery) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", HandlerWithDriverManager(applier, manager, windows, driverManager))
	writeStatus := func(w http.ResponseWriter, status RecoveryStatus) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
	}
	mux.HandleFunc("GET /storage-recovery", func(w http.ResponseWriter, r *http.Request) {
		if recovery == nil {
			http.Error(w, "storage recovery unavailable", http.StatusServiceUnavailable)
			return
		}
		writeStatus(w, recovery.Status())
	})
	mux.HandleFunc("PUT /storage-recovery", func(w http.ResponseWriter, r *http.Request) {
		if recovery == nil {
			http.Error(w, "storage recovery unavailable", http.StatusServiceUnavailable)
			return
		}
		var cfg RecoveryConfig
		if !decodeAgentRequest(w, r, &cfg, 4096) {
			return
		}
		if err := recovery.SetConfig(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeStatus(w, recovery.Status())
	})
	mux.HandleFunc("POST /storage-recovery/run", func(w http.ResponseWriter, r *http.Request) {
		if recovery == nil {
			http.Error(w, "storage recovery unavailable", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		writeStatus(w, recovery.RunOnce(ctx))
	})
	return mux
}
