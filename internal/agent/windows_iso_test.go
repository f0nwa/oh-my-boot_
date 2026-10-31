package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeWindowsISOMounter struct {
	mounted bool
	source  string
	target  string
	undo    bool
}

type fakeWindowsISOSystemctl struct {
	calls     [][]string
	mountRoot string
	assetRoot string
}

func (f *fakeWindowsISOSystemctl) Run(_ context.Context, args ...string) error {
	f.calls = append(f.calls, append([]string(nil), args...))
	if len(args) > 0 && args[0] == "enable" {
		root := filepath.Join(f.mountRoot, "17")
		for _, name := range []string{"efi/boot/bootx64.efi", "sources/boot.wim", "sources/setup.exe", "sources/install.wim"} {
			file := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(file, []byte("x"), 0444); err != nil {
				return err
			}
		}
		for _, name := range []string{"boot.wim", "winpeshl.ini", "omb-setup.cmd"} {
			file := filepath.Join(f.assetRoot, "17", name)
			if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(file, []byte("x"), 0444); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestWindowsISOManagerStoresImmutableConfigAndStartsIDUnit(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "key7")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	iso := filepath.Join(storage, "win10.iso")
	if err := os.WriteFile(iso, []byte("ISO"), 0444); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(iso)
	if err != nil {
		t.Fatal(err)
	}
	mountRoot := t.TempDir()
	configRoot := t.TempDir()
	assetRoot := t.TempDir()
	systemctl := &fakeWindowsISOSystemctl{mountRoot: mountRoot, assetRoot: assetRoot}
	manager := WindowsISOManager{SourceRoot: base, ConfigRoot: configRoot, MountRoot: mountRoot, AssetRoot: assetRoot, Systemctl: systemctl, Mounter: &fakeWindowsISOMounter{mounted: true}}
	req := WindowsISORequest{ID: 17, StorageRoot: storage, RelativePath: "win10.iso", Size: info.Size(), Modified: info.ModTime()}
	result, err := manager.Prepare(context.Background(), req)
	if err != nil || result.InstallFile != "install.wim" {
		t.Fatalf("managed preparation: %+v %v", result, err)
	}
	config, err := os.ReadFile(filepath.Join(configRoot, "17.json"))
	if err != nil || !strings.Contains(string(config), `"relative_path":"win10.iso"`) {
		t.Fatalf("profile config missing source: %q %v", config, err)
	}
	if len(systemctl.calls) != 1 || strings.Join(systemctl.calls[0], " ") != "enable --now omb-winiso@17.service" {
		t.Fatalf("unexpected systemctl calls: %+v", systemctl.calls)
	}
	if err := manager.Check(context.Background(), req); err != nil {
		t.Fatalf("prepared profile was unavailable: %v", err)
	}
	if err := os.Remove(filepath.Join(assetRoot, "17", "boot.wim")); err != nil {
		t.Fatal(err)
	}
	if err := manager.Check(context.Background(), req); err == nil {
		t.Fatal("accepted profile without boot.wim")
	}
	changed := req
	other := filepath.Join(storage, "other.iso")
	if err := os.WriteFile(other, []byte("ISO"), 0444); err != nil {
		t.Fatal(err)
	}
	otherInfo, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	changed.RelativePath = "other.iso"
	changed.Size = otherInfo.Size()
	changed.Modified = otherInfo.ModTime()
	if _, err := manager.Prepare(context.Background(), changed); err == nil {
		t.Fatal("replaced an existing profile source")
	}
}

func (f *fakeWindowsISOMounter) IsMounted(context.Context, string) (bool, error) {
	return f.mounted, nil
}
func (f *fakeWindowsISOMounter) Mount(_ context.Context, source, target string) error {
	f.source, f.target, f.mounted = source, target, true
	for _, name := range []string{"efi/boot/bootx64.efi", "sources/boot.wim", "sources/setup.exe", "sources/install.wim"} {
		file := filepath.Join(target, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(file, []byte("x"), 0444); err != nil {
			return err
		}
	}
	return nil
}
func (f *fakeWindowsISOMounter) Unmount(context.Context, string) error {
	f.undo, f.mounted = true, false
	return nil
}

func TestCommandWindowsISOMounterRecognizesUnmountedDirectory(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "mountpoint"), []byte("#!/bin/sh\nexit 32\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	mounted, err := (CommandWindowsISOMounter{}).IsMounted(context.Background(), t.TempDir())
	if err != nil || mounted {
		t.Fatalf("unmounted directory: mounted=%v, err=%v", mounted, err)
	}
}

func TestWindowsISORuntimePreparesSelectedSourceAtStableID(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "key7")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	iso := filepath.Join(storage, "win10.iso")
	if err := os.WriteFile(iso, []byte("ISO"), 0444); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(iso)
	if err != nil {
		t.Fatal(err)
	}
	req := WindowsISORequest{ID: 17, StorageRoot: storage, RelativePath: "win10.iso", Size: info.Size(), Modified: info.ModTime()}
	mounter := &fakeWindowsISOMounter{}
	runtime := WindowsISORuntime{SourceRoot: base, MountRoot: t.TempDir(), AssetRoot: t.TempDir(), Template: []byte("__OMB_ISO_PASSWORD__ __OMB_PROFILE_ID__ __OMB_INSTALL_FILE__"), Winpeshl: []byte("launch"), Password: strings.Repeat("A", 32), Mounter: mounter}
	result, err := runtime.Prepare(context.Background(), req)
	if err != nil || result.InstallFile != "install.wim" {
		t.Fatalf("preparation: %+v %v", result, err)
	}
	if filepath.Base(mounter.target) != "17" || filepath.Base(mounter.source) != "win10.iso" {
		t.Fatalf("wrong source or target: %+v", mounter)
	}
	if _, err := os.Stat(filepath.Join(runtime.AssetRoot, "17", "boot.wim")); err != nil {
		t.Fatalf("derived boot file is unavailable: %v", err)
	}
}

func TestResolveWindowsISOSourceRejectsChangedOrEscapingFiles(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "key7")
	if err := os.MkdirAll(filepath.Join(storage, "Windows"), 0755); err != nil {
		t.Fatal(err)
	}
	iso := filepath.Join(storage, "Windows", "win10.iso")
	if err := os.WriteFile(iso, []byte("ISO"), 0444); err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2025, 8, 26, 6, 9, 33, 378684300, time.UTC)
	if err := os.Chtimes(iso, modified, modified); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(iso)
	if err != nil {
		t.Fatal(err)
	}
	req := WindowsISORequest{ID: 17, StorageRoot: storage, RelativePath: "Windows/win10.iso", Size: info.Size(), Modified: info.ModTime().Truncate(time.Microsecond)}
	got, err := resolveWindowsISOSource(base, req)
	expected, resolveErr := filepath.EvalSymlinks(iso)
	if err != nil || resolveErr != nil || got != expected {
		t.Fatalf("valid catalog source was not resolved: %q %v", got, err)
	}
	for _, path := range []string{"../outside.iso", "/etc/passwd", "Windows/../../outside.iso", "Windows\\win10.iso"} {
		bad := req
		bad.RelativePath = path
		if _, err := resolveWindowsISOSource(base, bad); err == nil {
			t.Errorf("accepted escaping path %q", path)
		}
	}
	changed := req
	changed.Size++
	if _, err := resolveWindowsISOSource(base, changed); err == nil {
		t.Fatal("accepted changed ISO size")
	}
	changed = req
	changed.Modified = req.Modified.Add(time.Second)
	if _, err := resolveWindowsISOSource(base, changed); err == nil {
		t.Fatal("accepted changed ISO modification time")
	}
	if err := os.Symlink(iso, filepath.Join(storage, "Windows", "linked.iso")); err != nil {
		t.Fatal(err)
	}
	linked := req
	linked.RelativePath = "Windows/linked.iso"
	if _, err := resolveWindowsISOSource(base, linked); err == nil {
		t.Fatal("accepted symlink ISO")
	}
}

func TestPrepareWindowsISOAssetsCopiesOnlyBootWIMAndRendersProfile(t *testing.T) {
	mounted := t.TempDir()
	for name, content := range map[string]string{
		"efi/boot/bootx64.efi": "efi",
		"sources/boot.wim":     "boot-image",
		"sources/setup.exe":    "setup",
		"sources/install.wim":  "large-install-image",
	} {
		file := filepath.Join(mounted, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0444); err != nil {
			t.Fatal(err)
		}
	}
	assets := t.TempDir()
	template := []byte("@echo off\r\nset PASSWORD=__OMB_ISO_PASSWORD__\r\nY:\\__OMB_PROFILE_ID__\\sources\\setup.exe /InstallFrom:Y:\\__OMB_PROFILE_ID__\\sources\\__OMB_INSTALL_FILE__\r\n")
	installFile, err := prepareWindowsISOAssets(mounted, assets, 17, template, []byte("[LaunchApps]\r\nomb-setup.cmd\r\n"), strings.Repeat("A", 32))
	if err != nil || installFile != "install.wim" {
		t.Fatalf("asset preparation: %q %v", installFile, err)
	}
	boot, err := os.ReadFile(filepath.Join(assets, "17", "boot.wim"))
	if err != nil || string(boot) != "boot-image" {
		t.Fatalf("boot.wim differs from ISO: %q %v", boot, err)
	}
	script, err := os.ReadFile(filepath.Join(assets, "17", "omb-setup.cmd"))
	if err != nil || !strings.Contains(string(script), `Y:\17\sources\install.wim`) {
		t.Fatalf("startup script missing profile path: %q %v", script, err)
	}
	if _, err := os.Stat(filepath.Join(assets, "17", "install.wim")); !os.IsNotExist(err) {
		t.Fatalf("full installation image was copied: %v", err)
	}
	profileDir := filepath.Join(assets, "17")
	info, err := os.Stat(profileDir)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("Boot cannot traverse generated profile directory: info=%v err=%v", info, err)
	}
	if err := os.Chmod(profileDir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareWindowsISOAssets(mounted, assets, 17, template, []byte("[LaunchApps]\r\nomb-setup.cmd\r\n"), strings.Repeat("A", 32)); err != nil {
		t.Fatalf("re-prepare existing profile: %v", err)
	}
	info, err = os.Stat(profileDir)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("re-prepare did not restore Boot access: info=%v err=%v", info, err)
	}
}

func TestPrepareWindowsISOAssetsRejectsExistingSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"efi/boot/bootx64.efi", "sources/boot.wim", "sources/setup.exe", "sources/install.wim"} {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("x"), 0444); err != nil {
			t.Fatal(err)
		}
	}
	assets := t.TempDir()
	outside := t.TempDir()
	for _, name := range []string{"boot.wim", "winpeshl.ini", "omb-setup.cmd"} {
		if err := os.WriteFile(filepath.Join(outside, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(assets, "17")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareWindowsISOAssets(root, assets, 17, []byte("__OMB_ISO_PASSWORD__ __OMB_PROFILE_ID__ __OMB_INSTALL_FILE__"), []byte("x"), strings.Repeat("A", 32)); err == nil {
		t.Fatal("accepted symlink target for generated assets")
	}
}

func TestInspectMountedWindowsISORequiresX64SetupAndInstallImage(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"efi/boot/bootx64.efi", "sources/boot.wim", "sources/setup.exe", "sources/install.esd"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("data"), 0444); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := inspectMountedWindowsISO(root); err != nil || got != "install.esd" {
		t.Fatalf("ESD media inspection = %q %v", got, err)
	}
	if err := os.Remove(filepath.Join(root, "efi/boot/bootx64.efi")); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectMountedWindowsISO(root); err == nil {
		t.Fatal("accepted media without x64 UEFI loader")
	}
}

func TestWindowsISOManagerAcceptsSameModifiedInstantFromAnotherZone(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "key7")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	iso := filepath.Join(storage, "win10.iso")
	if err := os.WriteFile(iso, []byte("ISO"), 0444); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(iso)
	if err != nil {
		t.Fatal(err)
	}
	mountRoot, configRoot, assetRoot := t.TempDir(), t.TempDir(), t.TempDir()
	systemctl := &fakeWindowsISOSystemctl{mountRoot: mountRoot, assetRoot: assetRoot}
	manager := WindowsISOManager{SourceRoot: base, ConfigRoot: configRoot, MountRoot: mountRoot, AssetRoot: assetRoot, Systemctl: systemctl, Mounter: &fakeWindowsISOMounter{mounted: true}}
	req := WindowsISORequest{ID: 17, StorageRoot: storage, RelativePath: "win10.iso", Size: info.Size(), Modified: info.ModTime().UTC()}
	if _, err := manager.Prepare(context.Background(), req); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	moscow := req
	moscow.Modified = req.Modified.In(time.FixedZone("MSK", 3*60*60))
	if err := manager.Check(context.Background(), moscow); err != nil {
		t.Fatalf("same instant in another zone rejected by Check: %v", err)
	}
	changed := req
	changed.Size++
	if err := manager.Check(context.Background(), changed); err == nil {
		t.Fatal("accepted changed source")
	}
}

func TestStoredWindowsISORequestComparesModifiedAsInstant(t *testing.T) {
	stored := []byte(`{"id":1,"storage_root":"/mnt/s","relative_path":"win.iso","size":7,"modified":"2025-08-26T06:09:33.378684Z"}`)
	utc := time.Date(2025, 8, 26, 6, 9, 33, 378684000, time.UTC)
	req := WindowsISORequest{ID: 1, StorageRoot: "/mnt/s", RelativePath: "win.iso", Size: 7, Modified: utc.In(time.FixedZone("MSK", 3*60*60))}
	if !sameStoredWindowsISORequest(stored, req) {
		t.Fatal("same instant in another zone differs")
	}
	later := req
	later.Modified = req.Modified.Add(time.Second)
	if sameStoredWindowsISORequest(stored, later) {
		t.Fatal("different modification time accepted")
	}
	other := req
	other.RelativePath = "other.iso"
	if sameStoredWindowsISORequest(stored, other) || sameStoredWindowsISORequest([]byte("{"), req) {
		t.Fatal("different or broken configuration accepted")
	}
}

func TestResolveWindowsISOSourceAcceptsLocalStorage(t *testing.T) {
	smbRoot, local, outside := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(dir string) WindowsISORequest {
		if err := os.MkdirAll(filepath.Join(dir, "iso"), 0o755); err != nil {
			t.Fatal(err)
		}
		iso := filepath.Join(dir, "iso", "win11.iso")
		if err := os.WriteFile(iso, []byte("iso"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(iso)
		return WindowsISORequest{ID: 4, StorageRoot: filepath.Join(dir, "iso"), RelativePath: "win11.iso", Size: info.Size(), Modified: info.ModTime()}
	}
	req := write(local)
	if _, err := resolveWindowsISOSource(smbRoot, req, local); err != nil {
		t.Fatalf("local storage rejected: %v", err)
	}
	if _, err := resolveWindowsISOSource(smbRoot, req); err == nil {
		t.Fatal("local storage accepted without a local root")
	}
	if _, err := resolveWindowsISOSource(smbRoot, write(outside), local); err == nil {
		t.Fatal("storage outside both roots accepted")
	}
}
