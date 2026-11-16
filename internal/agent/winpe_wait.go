package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"omb/internal/winpe"
)

// DriverScriptUpdate reports the startup script checksum of a prepared driver
// configuration after applying WinPE wait settings.
type DriverScriptUpdate struct {
	ConfigID int64  `json:"config_id"`
	OldSHA   string `json:"old_sha"`
	NewSHA   string `json:"new_sha"`
}

// WinPEWaitUpdater updates the WinPE startup template and all prepared profile,
// Strelec, and driver-configuration startup scripts.
type WinPEWaitUpdater struct {
	CustomTemplatePath  string
	DefaultTemplatePath string
	ProfileRoot         string
	DriverConfigRoot    string
}

type fileMutation struct {
	path       string
	mode       os.FileMode
	existed    bool
	oldContent []byte
	newContent []byte
}

// UpdateWinPEScripts applies the given attempt limit and retry pause to the
// template and all prepared scripts, returning driver-config checksum updates.
func (a *Applier) UpdateWinPEScripts(ctx context.Context, attempts, retrySeconds int) ([]DriverScriptUpdate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.WinPE == nil {
		return nil, nil
	}
	updates, _, err := a.WinPE.apply(ctx, attempts, retrySeconds)
	return updates, err
}

// LastDriverUpdates returns the driver script checksums from the most recent Apply.
func (a *Applier) LastDriverUpdates() []DriverScriptUpdate {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]DriverScriptUpdate, len(a.lastDriverUpdates))
	copy(out, a.lastDriverUpdates)
	return out
}

func (u *WinPEWaitUpdater) apply(ctx context.Context, attempts, retrySeconds int) ([]DriverScriptUpdate, func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	var mutations []fileMutation

	tmpl, err := ResolveWinPETemplate(u.CustomTemplatePath, u.DefaultTemplatePath)
	if err != nil {
		return nil, nil, fmt.Errorf("read WinPE template: %w", err)
	}
	newTmpl, _, err := winpe.UpdateStartupWait(tmpl, attempts, retrySeconds)
	if err != nil {
		return nil, nil, fmt.Errorf("update WinPE template: %w", err)
	}
	var oldCustom []byte
	customExisted := false
	if info, err := os.Lstat(u.CustomTemplatePath); err == nil {
		if !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("custom WinPE template is not a regular file")
		}
		oldCustom, err = os.ReadFile(u.CustomTemplatePath)
		if err != nil {
			return nil, nil, err
		}
		customExisted = true
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	if !customExisted || string(oldCustom) != string(newTmpl) {
		mutations = append(mutations, fileMutation{
			path:       u.CustomTemplatePath,
			mode:       0o644,
			existed:    customExisted,
			oldContent: oldCustom,
			newContent: newTmpl,
		})
	}

	profileMutations, err := planScriptDirUpdates(u.ProfileRoot, attempts, retrySeconds)
	if err != nil {
		return nil, nil, err
	}
	mutations = append(mutations, profileMutations...)

	driverMutations, updates, err := planDriverConfigUpdates(u.DriverConfigRoot, attempts, retrySeconds)
	if err != nil {
		return nil, nil, err
	}
	mutations = append(mutations, driverMutations...)

	var applied []fileMutation
	rollback := func() error {
		var errs []error
		for i := len(applied) - 1; i >= 0; i-- {
			m := applied[i]
			if !m.existed {
				if err := os.Remove(m.path); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
				continue
			}
			if err := writeAtomic(m.path, m.oldContent, m.mode); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	for _, m := range mutations {
		if err := writeAtomic(m.path, m.newContent, m.mode); err != nil {
			return nil, nil, errors.Join(fmt.Errorf("write %s: %w", m.path, err), rollback())
		}
		applied = append(applied, m)
	}
	return updates, rollback, nil
}

func planScriptDirUpdates(root string, attempts, retrySeconds int) ([]fileMutation, error) {
	if root == "" {
		return nil, nil
	}
	ids, err := numericSubdirs(root)
	if err != nil {
		return nil, err
	}
	var mutations []fileMutation
	for _, id := range ids {
		for _, scriptName := range []string{"omb-setup.cmd", "omb-answer-setup.cmd"} {
			scriptPath := filepath.Join(root, strconv.FormatInt(id, 10), scriptName)
			info, err := os.Lstat(scriptPath)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() || info.Size() == 0 {
				return nil, fmt.Errorf("WinPE script %s is not a regular non-empty file", scriptPath)
			}
			oldBytes, err := os.ReadFile(scriptPath)
			if err != nil {
				return nil, err
			}
			newBytes, changed, err := winpe.UpdateStartupWait(oldBytes, attempts, retrySeconds)
			if err != nil {
				return nil, fmt.Errorf("update %s: %w", scriptPath, err)
			}
			if changed {
				mutations = append(mutations, fileMutation{
					path:       scriptPath,
					mode:       info.Mode().Perm(),
					existed:    true,
					oldContent: oldBytes,
					newContent: newBytes,
				})
			}
		}
	}
	return mutations, nil
}

func planDriverConfigUpdates(root string, attempts, retrySeconds int) ([]fileMutation, []DriverScriptUpdate, error) {
	if root == "" {
		return nil, nil, nil
	}
	ids, err := numericSubdirs(root)
	if err != nil {
		return nil, nil, err
	}
	var mutations []fileMutation
	var updates []DriverScriptUpdate
	for _, id := range ids {
		dir := filepath.Join(root, strconv.FormatInt(id, 10))
		scriptPath := filepath.Join(dir, "omb-setup.cmd")
		manifestPath := filepath.Join(dir, "build.json")

		scriptInfo, err := os.Lstat(scriptPath)
		if err != nil {
			return nil, nil, fmt.Errorf("stat driver script %d: %w", id, err)
		}
		if !scriptInfo.Mode().IsRegular() || scriptInfo.Size() == 0 {
			return nil, nil, fmt.Errorf("driver script %d is not a regular file", id)
		}
		manifestInfo, err := os.Lstat(manifestPath)
		if err != nil {
			return nil, nil, fmt.Errorf("stat driver manifest %d: %w", id, err)
		}
		if !manifestInfo.Mode().IsRegular() || manifestInfo.Size() == 0 {
			return nil, nil, fmt.Errorf("driver manifest %d is not a regular file", id)
		}

		oldScript, err := os.ReadFile(scriptPath)
		if err != nil {
			return nil, nil, err
		}
		oldManifest, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, nil, err
		}
		var manifest driverConfigManifest
		if err := json.Unmarshal(oldManifest, &manifest); err != nil {
			return nil, nil, fmt.Errorf("parse driver manifest %d: %w", id, err)
		}
		oldSum := sha256.Sum256(oldScript)
		oldSHA := hex.EncodeToString(oldSum[:])
		if manifest.Request.ConfigID != id || manifest.Result.ScriptSHA256 != oldSHA {
			return nil, nil, fmt.Errorf("driver manifest %d identity or script checksum mismatch", id)
		}

		newScript, changed, err := winpe.UpdateStartupWait(oldScript, attempts, retrySeconds)
		if err != nil {
			return nil, nil, fmt.Errorf("update driver script %d: %w", id, err)
		}
		newSum := sha256.Sum256(newScript)
		newSHA := hex.EncodeToString(newSum[:])
		updates = append(updates, DriverScriptUpdate{
			ConfigID: id,
			OldSHA:   oldSHA,
			NewSHA:   newSHA,
		})
		if changed {
			manifest.Result.ScriptSHA256 = newSHA
			newManifest, err := json.Marshal(manifest)
			if err != nil {
				return nil, nil, err
			}
			mutations = append(mutations,
				fileMutation{
					path:       scriptPath,
					mode:       scriptInfo.Mode().Perm(),
					existed:    true,
					oldContent: oldScript,
					newContent: newScript,
				},
				fileMutation{
					path:       manifestPath,
					mode:       manifestInfo.Mode().Perm(),
					existed:    true,
					oldContent: oldManifest,
					newContent: newManifest,
				},
			)
		}
	}
	return mutations, updates, nil
}

func numericSubdirs(root string) ([]int64, error) {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("asset root %s is not a directory", root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, entry := range entries {
		name := entry.Name()
		if len(name) == 0 || name[0] == '.' {
			continue
		}
		id, err := strconv.ParseInt(name, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != name {
			continue
		}
		childInfo, err := os.Lstat(filepath.Join(root, name))
		if err != nil {
			return nil, err
		}
		if !childInfo.IsDir() || childInfo.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("asset entry %s/%s is not a regular directory", root, name)
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}
