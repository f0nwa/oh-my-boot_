package agent

import (
	"context"
	"fmt"
	"omb/internal/winpe/winpetest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type strelecMounterFixture struct{}

func (strelecMounterFixture) IsMounted(context.Context, string) (bool, error) { return false, nil }
func (strelecMounterFixture) Unmount(context.Context, string) error           { return nil }
func (strelecMounterFixture) Mount(_ context.Context, source, target string) error {
	for _, name := range []string{"SSTR/strelec11x64.wim", "SSTR/MInst/MInst.exe", "SSTR/MInst/Portable/x64/Files_11_x64.exe", "SSTR/bootmgr", "SSTR/boot.sdi", "SSTR/BCD", "EFI/boot/bootx64.efi"} {
		path := filepath.Join(target, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		data := []byte("media")
		if name == "SSTR/BCD" {
			data = winpetest.StrelecBCD()
		}
		if err := os.WriteFile(path, data, 0444); err != nil {
			return err
		}
	}
	return nil
}

type strelecWIMFixture struct {
	wim       string
	malformed bool
}

func (f *strelecWIMFixture) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	if name != "wimlib-imagex" || len(args) < 2 || args[1] != f.wim {
		return nil, fmt.Errorf("wrong WIM command: %s %v", name, args)
	}
	switch args[0] {
	case "info":
		return []byte("Image Count: 1\nBoot Index: 1\nArchitecture: x86_64\n"), nil
	case "extract":
		if len(args) != 5 || args[2] != "1" || args[4] != "--to-stdout" {
			return nil, fmt.Errorf("wrong extraction arguments")
		}
		switch args[3] {
		case "/Windows/System32/pecmdmain.ini":
			return []byte("LOAD %SystemRoot%\\system32\\pecmd.ini\r\n//LOAD %SystemRoot%\\system32\\PecmdPXE.ini\r\n"), nil
		case "/Windows/System32/PecmdPXE.ini":
			return []byte("SHEL %Windir%\\explorer.exe\r\nEXEC !%windir%\\system32\\network_pxe.cmd\r\n"), nil
		case "/Windows/System32/network_pxe.cmd":
			if f.malformed {
				return []byte("no network mapping"), nil
			}
			return []byte("net start Netman\r\nnet use Z: \\\\STRELEC-PC\\H /USER:demo demo\r\nZ:\\SSTR\\MInst\\Portable\\x64\\Files_11_x64.exe\r\nexit\r\n"), nil
		}
	}
	return nil, fmt.Errorf("unexpected WIM command")
}

func strelecISOFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"SSTR/strelec11x64.wim", "SSTR/MInst/MInst.exe", "SSTR/MInst/Portable/x64/Files_11_x64.exe", "SSTR/bootmgr", "SSTR/boot.sdi", "SSTR/BCD", "EFI/boot/bootx64.efi"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		data := []byte("media")
		if name == "SSTR/BCD" {
			data = winpetest.StrelecBCD()
		}
		if err := os.WriteFile(p, data, 0444); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestStrelecPreparationUsesNativePXEWithoutWindowsSetup(t *testing.T) {
	root := strelecISOFixture(t)
	assets := t.TempDir()
	runner := &strelecWIMFixture{wim: filepath.Join(root, "SSTR", "strelec11x64.wim")}
	template := []byte("net use Y: \\\\10.30.0.10\\ombisos __OMB_ISO_PASSWORD__ /user:omb-iso\r\n")
	if err := prepareStrelecISOAssets(context.Background(), root, assets, 17, template, strings.Repeat("A", 32), runner); err != nil {
		t.Fatal(err)
	}
	boot, err := os.ReadFile(filepath.Join(assets, "17", "boot.wim"))
	if err != nil || string(boot) != "media" {
		t.Fatalf("WIM changed: %q %v", boot, err)
	}
	for _, name := range []string{"bootmgr", "bootx64.efi", "BCD", "boot.sdi"} {
		if _, err := os.Stat(filepath.Join(assets, "17", name)); err != nil {
			t.Fatalf("service WIM has no embedded boot manager; external %s missing: %v", name, err)
		}
	}
	main, err := os.ReadFile(filepath.Join(assets, "17", "pecmdmain.ini"))
	if err != nil || !strings.Contains(string(main), "LOAD %SystemRoot%\\system32\\PecmdPXE.ini") {
		t.Fatalf("native PXE not selected: %q %v", main, err)
	}
	script, err := os.ReadFile(filepath.Join(assets, "17", "omb-setup.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), `\\10.30.0.10\ombisos\17`) || !strings.Contains(string(script), `Files_11_x64.exe`) || strings.Contains(string(script), "setup.exe") {
		t.Fatalf("incorrect service startup: %s", script)
	}
	if _, err := os.Stat(filepath.Join(assets, "17", "winpeshl.ini")); !os.IsNotExist(err) {
		t.Fatal("service preparation replaced native winpeshl")
	}
}

func TestStrelecPreparationRejectsBrokenRecipeWithoutPublishing(t *testing.T) {
	root := strelecISOFixture(t)
	assets := t.TempDir()
	runner := &strelecWIMFixture{wim: filepath.Join(root, "SSTR", "strelec11x64.wim"), malformed: true}
	template := []byte("net use Y: \\\\10.30.0.10\\ombisos __OMB_ISO_PASSWORD__ /user:omb-iso\r\n")
	if err := prepareStrelecISOAssets(context.Background(), root, assets, 17, template, strings.Repeat("A", 32), runner); err == nil {
		t.Fatal("accepted broken native PXE")
	}
	if _, err := os.Stat(filepath.Join(assets, "17")); !os.IsNotExist(err) {
		t.Fatal("published partial service assets")
	}
}

func TestStrelecRuntimeAcceptsServiceISOWithoutInstallerFiles(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "key7")
	if err := os.Mkdir(storage, 0755); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(storage, "Strelec.iso")
	if err := os.WriteFile(image, []byte("ISO"), 0444); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(image)
	if err != nil {
		t.Fatal(err)
	}
	mount := t.TempDir()
	assets := t.TempDir()
	req := WindowsISORequest{ID: 17, BootMode: ISOStrelecMode, StorageRoot: storage, RelativePath: "Strelec.iso", Size: info.Size(), Modified: info.ModTime()}
	runtime := WindowsISORuntime{SourceRoot: base, MountRoot: mount, AssetRoot: assets, Mounter: strelecMounterFixture{}, Password: strings.Repeat("A", 32), Template: []byte("net use Y: \\\\10.30.0.10\\ombisos __OMB_ISO_PASSWORD__ /user:omb-iso\r\n"), WIMCommands: &strelecWIMFixture{wim: filepath.Join(mount, "17", "SSTR", "strelec11x64.wim")}}
	result, err := runtime.Prepare(context.Background(), req)
	if err != nil || result.InstallFile != "" {
		t.Fatalf("service preparation: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(assets, "17", "boot.wim")); err != nil {
		t.Fatal(err)
	}
	req.BootMode = "arbitrary"
	if _, err := runtime.Prepare(context.Background(), req); err == nil {
		t.Fatal("accepted unknown boot mode")
	}
}

func TestStrelecInspectionRejectsSymlinkedPayload(t *testing.T) {
	root := strelecISOFixture(t)
	outside := t.TempDir()
	if err := os.Rename(filepath.Join(root, "SSTR"), filepath.Join(outside, "SSTR")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "SSTR"), filepath.Join(root, "SSTR")); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectMountedStrelecISO(root); err == nil {
		t.Fatal("followed a symlink outside the ISO")
	}
}
