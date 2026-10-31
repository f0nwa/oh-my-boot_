package ombmigration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"omb/internal/agent"
)

type BuildInput struct {
	Root, Stage string
	Manifest    Manifest
	Source      Snapshot
	XML         map[int64][]byte
}
type BuildResult struct {
	Answers map[int64]AnswerChange
	Drivers map[int64]DriverChange
}
type Rebuilder interface {
	Rebuild(context.Context, BuildInput) (BuildResult, error)
}
type RuntimeRebuilder struct{ Runner Runner }
type mountedOnly struct{ runner Runner }

func (m mountedOnly) IsMounted(ctx context.Context, p string) (bool, error) {
	_, e := m.runner.Run(ctx, "mountpoint", []string{"-q", "--", p}, nil)
	if e != nil {
		return false, fmt.Errorf("required ISO mount unavailable")
	}
	return true, nil
}
func (m mountedOnly) Mount(context.Context, string, string) error {
	return fmt.Errorf("migration cannot create ISO mounts")
}
func (m mountedOnly) Unmount(context.Context, string) error {
	return fmt.Errorf("migration cannot remove ISO mounts")
}

type driverRunner struct{ runner Runner }

func (d driverRunner) Run(c context.Context, input, name string, args ...string) ([]byte, error) {
	return d.runner.Run(c, name, args, []byte(input))
}
func (r RuntimeRebuilder) Rebuild(ctx context.Context, in BuildInput) (BuildResult, error) {
	result := BuildResult{Answers: map[int64]AnswerChange{}, Drivers: map[int64]DriverChange{}}
	runner := r.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	resolve := func(p string) string { v, _ := safePath(in.Root, p); return v }
	stage := func(n string) string { return filepath.Join(in.Stage, n) }
	profiles := stage("windows-profiles")
	answers := stage("windows-answers")
	if len(in.Manifest.Profiles) > 0 {
		template, e := os.ReadFile(resolve("/usr/share/omb/windows-profiles/omb-setup.cmd.in"))
		if e != nil {
			return result, fmt.Errorf("profile template unavailable")
		}
		shell, e := os.ReadFile(resolve("/usr/share/omb/windows-profiles/winpeshl.ini"))
		if e != nil {
			return result, fmt.Errorf("WinPE shell unavailable")
		}
		secret, e := os.ReadFile(resolve("/etc/omb/omb-iso-password"))
		if e != nil {
			return result, fmt.Errorf("installation secret unavailable")
		}
		runtime := agent.WindowsISORuntime{SourceRoot: resolve("/mnt/omb-smb"), LocalRoot: resolve("/var/lib/omb-images"), MountRoot: resolve("/mnt/omb-iso/profiles"), AssetRoot: profiles, Template: template, Winpeshl: shell, Password: trimSecret(secret), Mounter: mountedOnly{runner}}
		for _, req := range in.Manifest.ProfileConfigs {
			row := in.Source.find("pxe_control", "windows_profiles", req.ID)
			if text(row, "state") != "ready" {
				continue
			}
			req.StorageRoot = resolve(MapPath(req.StorageRoot))
			if e := os.RemoveAll(filepath.Join(profiles, strconv.FormatInt(req.ID, 10))); e != nil {
				return result, e
			}
			res, e := runtime.Prepare(ctx, req)
			if e != nil {
				return result, fmt.Errorf("profile rebuild failed: %d", req.ID)
			}
			if expected := text(row, "install_file"); expected != "" && res.InstallFile != expected {
				return result, fmt.Errorf("profile install image differs: %d", req.ID)
			}
		}
	}
	manager := &agent.WindowsISOManager{AssetRoot: profiles, AnswerRoot: answers}
	for _, old := range in.Manifest.Answers {
		id := number(old, "id")
		if text(old, "state") != "ready" {
			result.Answers[id] = AnswerChange{ProfileID: number(old, "profile_id"), OldHash: text(old, "sha256"), NewHash: text(old, "sha256")}
			continue
		}
		xml := in.XML[id]
		digest := hashBytes(xml)
		if e := os.RemoveAll(filepath.Join(answers, strconv.FormatInt(id, 10))); e != nil {
			return result, e
		}
		if e := manager.PrepareAnswer(ctx, agent.WindowsAnswerRequest{ID: id, ProfileID: number(old, "profile_id"), SHA256: digest, XML: xml}); e != nil {
			return result, fmt.Errorf("answer rebuild failed: %d", id)
		}
		result.Answers[id] = AnswerChange{ProfileID: number(old, "profile_id"), OldHash: text(old, "sha256"), NewHash: digest}
	}
	drivers := &agent.DriverManager{Root: resolve("/var/lib/omb-agent/drivers"), ProfileRoot: profiles, AnswerRoot: answers, WIMRoot: stage("windows-driver-wims"), ConfigRoot: stage("windows-driver-configs"), Runner: driverRunner{runner}}
	// Legacy cache keys omit the product's embedded driver directory. Even a
	// matching key/hash can still contain pxe-drivers, so rebuild each ready key
	// once before any configurations reuse the new staged result.
	cleared := map[string]bool{}
	for _, old := range in.Manifest.Drivers {
		key := text(old, "wim_key")
		if text(old, "state") == "ready" && !cleared[key] {
			if !validHash(key) {
				return result, fmt.Errorf("invalid legacy WIM key")
			}
			if e := os.RemoveAll(filepath.Join(drivers.WIMRoot, key)); e != nil {
				return result, e
			}
			cleared[key] = true
		}
	}
	for _, old := range in.Manifest.Drivers {
		id := number(old, "id")
		change := DriverChange{ProfileID: number(old, "profile_id"), AnswerID: number(old, "answer_id"), OldKey: text(old, "wim_key"), OldWIM: text(old, "wim_sha256"), OldScript: text(old, "script_sha256")}
		if text(old, "state") != "ready" {
			change.NewKey = change.OldKey
			change.NewWIM = change.OldWIM
			change.NewScript = change.OldScript
			result.Drivers[id] = change
			continue
		}
		row := in.Source.find("pxe_control", "winpe_driver_configs", id)
		var selected []agent.SelectedINF
		if decodeJSON(mustJSON(row["selected_infs"]), &selected) != nil || len(selected) == 0 {
			return result, fmt.Errorf("invalid selected INFs: %d", id)
		}
		if e := os.RemoveAll(filepath.Join(drivers.ConfigRoot, strconv.FormatInt(id, 10))); e != nil {
			return result, e
		}
		built, e := drivers.PrepareConfig(ctx, agent.DriverBuildRequest{ConfigID: id, ProfileID: change.ProfileID, AnswerID: change.AnswerID, PackageINFs: selected})
		if e != nil {
			return result, fmt.Errorf("driver rebuild failed: %d", id)
		}
		change.NewKey = built.WIMKey
		change.NewWIM = built.WIMSHA256
		change.NewScript = built.ScriptSHA256
		result.Drivers[id] = change
	}
	return result, nil
}

func trimSecret(b []byte) string { return strings.TrimSpace(string(b)) }
