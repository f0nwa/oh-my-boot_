package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"omb/internal/winpe"
)

var strelecArchitecture = regexp.MustCompile(`(?m)^Architecture:\s*x86_64\s*$`)
var strelecSingleImage = regexp.MustCompile(`(?m)^Image Count:\s*1\s*$`)

var strelecBootInputs = []struct{ source, asset string }{
	{"SSTR/bootmgr", "bootmgr"},
	{"EFI/boot/bootx64.efi", "bootx64.efi"},
	{"SSTR/BCD", "BCD"},
	{"SSTR/boot.sdi", "boot.sdi"},
}

// Only documented Strelec layouts are supported. Never treat every ISO as an
// installer or silently use an arbitrary WIM found somewhere in the image.
func inspectMountedStrelecISO(root string) (string, error) {
	for _, input := range strelecBootInputs {
		if err := requireStrelecFile(root, input.source); err != nil {
			return "", err
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(input.source)))
		if err != nil || info.Size() > 16*1024*1024 {
			return "", fmt.Errorf("Strelec boot input exceeds size limit: %s", input.source)
		}
	}
	if err := requireStrelecFile(root, "SSTR/MInst/MInst.exe"); err != nil {
		return "", err
	}
	for _, version := range []string{"11", "10"} {
		name := "SSTR/strelec" + version + "x64.wim"
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); os.IsNotExist(err) {
			continue
		}
		if err := requireStrelecFile(root, name); err != nil {
			return "", err
		}
		if err := requireStrelecFile(root, "SSTR/MInst/Portable/x64/Files_"+version+"_x64.exe"); err != nil {
			return "", err
		}
		return name, nil
	}
	return "", fmt.Errorf("supported Strelec x64 WIM is missing")
}

func requireStrelecFile(root, name string) error {
	current := root
	rel := filepath.FromSlash(name)
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Strelec file is missing or has a symlink: %s", name)
		}
	}
	return requireRegularISOFile(root, name)
}

func inspectMountedISO(root, mode string) (string, error) {
	switch mode {
	case "":
		return inspectMountedWindowsISO(root)
	case ISOStrelecMode:
		_, err := inspectMountedStrelecISO(root)
		return "", err
	default:
		return "", fmt.Errorf("unsupported ISO boot mode")
	}
}

func isoAssetFiles(mode string) []string {
	if mode == ISOStrelecMode {
		return []string{"boot.wim", "pecmdmain.ini", "omb-setup.cmd", "bootmgr", "bootx64.efi", "BCD", "boot.sdi"}
	}
	return []string{"boot.wim", "winpeshl.ini", "omb-setup.cmd"}
}

func prepareStrelecISOAssets(ctx context.Context, root, assetRoot string, id int64, template []byte, password string, commands DriverCommandRunner) error {
	if id <= 0 {
		return fmt.Errorf("invalid Strelec source ID")
	}
	selected, err := inspectMountedStrelecISO(root)
	if err != nil {
		return err
	}
	wim := filepath.Join(root, filepath.FromSlash(selected))
	if commands == nil {
		commands = strelecWIMCommands{}
	}
	info, err := commands.Run(ctx, "", "wimlib-imagex", "info", wim)
	if err != nil {
		return err
	}
	boot := bootIndexLine.FindSubmatch(info)
	if !strelecSingleImage.Match(info) || !strelecArchitecture.Match(info) || len(boot) != 2 || string(boot[1]) != "1" {
		return fmt.Errorf("Strelec WIM must contain one bootable x64 image")
	}
	extract := func(name string) ([]byte, error) {
		output, err := commands.Run(ctx, "", "wimlib-imagex", "extract", wim, "1", "/Windows/System32/"+name, "--to-stdout")
		if err != nil {
			return nil, err
		}
		if len(output) == 0 || len(output) > 256*1024 || bytes.ContainsRune(output, 0) {
			return nil, fmt.Errorf("unsupported Strelec native configuration: %s", name)
		}
		return output, nil
	}
	main, err := extract("pecmdmain.ini")
	if err != nil {
		return err
	}
	native, err := extract("PecmdPXE.ini")
	if err != nil {
		return err
	}
	network, err := extract("network_pxe.cmd")
	if err != nil {
		return err
	}
	if !bytes.Contains(bytes.ToLower(main), []byte("pecmdpxe.ini")) || !bytes.Contains(bytes.ToLower(native), []byte("network_pxe.cmd")) {
		return fmt.Errorf("Strelec native PXE entry points are missing")
	}
	version := "11"
	if selected == "SSTR/strelec10x64.wim" {
		version = "10"
	}
	if !bytes.Contains(network, []byte("Files_"+version+"_x64.exe")) {
		return fmt.Errorf("Strelec program launcher does not match the selected WIM")
	}
	script, err := winpe.RenderStrelecNetwork(network, template, password, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(assetRoot, 0755); err != nil {
		return err
	}
	final := filepath.Join(assetRoot, strconv.FormatInt(id, 10))
	if info, err := os.Lstat(final); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("invalid Strelec asset directory")
		}
		for _, name := range isoAssetFiles(ISOStrelecMode) {
			if err := requireRegularISOFile(final, name); err != nil {
				return err
			}
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(assetRoot, ".strelec-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	source, err := os.Open(wim)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(filepath.Join(stage, "boot.wim"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.WriteFile(filepath.Join(stage, "pecmdmain.ini"), []byte("LOAD %SystemRoot%\\system32\\PecmdPXE.ini\r\n"), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "omb-setup.cmd"), script, 0644); err != nil {
		return err
	}
	// Strelec omits the boot manager, BCD and SDI from its WIM. These
	// must be supplied from the ISO, rather than relying on wimboot extraction.
	for _, input := range strelecBootInputs {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(input.source)))
		if err != nil {
			return err
		}
		if input.asset == "BCD" {
			data, err = winpe.RenderStrelecBCD(data, version)
			if err != nil {
				return err
			}
		}
		if err := os.WriteFile(filepath.Join(stage, input.asset), data, 0644); err != nil {
			return err
		}
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return err
	}
	return os.Rename(stage, final)
}

type boundedStrelecOutput struct{ bytes.Buffer }

func (b *boundedStrelecOutput) Write(p []byte) (int, error) {
	if len(p) > 256*1024-b.Len() {
		return 0, fmt.Errorf("WIM configuration exceeds output limit")
	}
	return b.Buffer.Write(p)
}

type strelecWIMCommands struct{}

func (strelecWIMCommands) Run(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	if name != "wimlib-imagex" || input != "" {
		return nil, fmt.Errorf("invalid Strelec WIM command")
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var output, diagnostics boundedStrelecOutput
	cmd.Stdout = &output
	cmd.Stderr = &diagnostics
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("Strelec WIM inspection failed: %w", err)
	}
	return output.Bytes(), nil
}
