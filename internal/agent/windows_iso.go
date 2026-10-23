package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"pxe-platform/internal/winpe"
)

type WindowsISORequest struct {
	ID           int64     `json:"id"`
	StorageRoot  string    `json:"storage_root"`
	RelativePath string    `json:"relative_path"`
	Size         int64     `json:"size"`
	Modified     time.Time `json:"modified"`
}

type WindowsISOResult struct {
	InstallFile string `json:"install_file"`
}

type WindowsISOMounter interface {
	IsMounted(context.Context, string) (bool, error)
	Mount(context.Context, string, string) error
	Unmount(context.Context, string) error
}

type CommandWindowsISOMounter struct{}

func (CommandWindowsISOMounter) IsMounted(ctx context.Context, target string) (bool, error) {
	err := exec.CommandContext(ctx, "mountpoint", "-q", target).Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	// util-linux mountpoint returns 32 when the directory is not mounted.
	if errors.As(err, &exit) && (exit.ExitCode() == 1 || exit.ExitCode() == 32) {
		return false, nil
	}
	return false, err
}

func (CommandWindowsISOMounter) Mount(ctx context.Context, source, target string) error {
	return run(ctx, "mount", "-o", "loop,ro,nosuid,nodev,noexec", source, target)
}

func (CommandWindowsISOMounter) Unmount(ctx context.Context, target string) error {
	return run(ctx, "umount", target)
}

type WindowsISORuntime struct {
	SourceRoot string
	LocalRoot  string
	MountRoot  string
	AssetRoot  string
	Template   []byte
	Winpeshl   []byte
	Password   string
	Mounter    WindowsISOMounter
}

func (runtime WindowsISORuntime) Prepare(ctx context.Context, req WindowsISORequest) (WindowsISOResult, error) {
	source, err := resolveWindowsISOSource(runtime.SourceRoot, req, runtime.LocalRoot)
	if err != nil {
		return WindowsISOResult{}, err
	}
	if runtime.Mounter == nil {
		return WindowsISOResult{}, fmt.Errorf("Windows ISO mounter is unavailable")
	}
	if err := os.MkdirAll(runtime.MountRoot, 0755); err != nil {
		return WindowsISOResult{}, err
	}
	target := filepath.Join(runtime.MountRoot, strconv.FormatInt(req.ID, 10))
	if info, err := os.Lstat(target); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return WindowsISOResult{}, fmt.Errorf("Windows ISO mount target is not a directory")
		}
	} else if os.IsNotExist(err) {
		if err := os.Mkdir(target, 0755); err != nil {
			return WindowsISOResult{}, err
		}
	} else {
		return WindowsISOResult{}, err
	}
	mounted, err := runtime.Mounter.IsMounted(ctx, target)
	if err != nil {
		return WindowsISOResult{}, err
	}
	newMount := !mounted
	if newMount {
		if err := runtime.Mounter.Mount(ctx, source, target); err != nil {
			return WindowsISOResult{}, err
		}
	}
	installFile, err := prepareWindowsISOAssets(target, runtime.AssetRoot, req.ID, runtime.Template, runtime.Winpeshl, runtime.Password)
	if err != nil {
		if newMount {
			_ = runtime.Mounter.Unmount(ctx, target)
		}
		return WindowsISOResult{}, err
	}
	return WindowsISOResult{InstallFile: installFile}, nil
}

// LocalISORoot holds the local ISO storages of this server; an ISO for a
// Windows profile comes either from an SMB storage or from under this root.
const LocalISORoot = "/var/lib/pxe-platform-images"

// resolveWindowsISOSource finds the ISO of a profile request. Its storage
// must be one SMB mount directly under allowedRoot, or localRoot or a
// directory inside it.
func resolveWindowsISOSource(allowedRoot string, req WindowsISORequest, localRoot ...string) (string, error) {
	if req.ID <= 0 || req.Size <= 0 || !filepath.IsAbs(req.StorageRoot) || req.RelativePath == "" || path.IsAbs(req.RelativePath) || path.Clean(req.RelativePath) != req.RelativePath || strings.ContainsAny(req.RelativePath, "\\\x00") || !strings.EqualFold(path.Ext(req.RelativePath), ".iso") {
		return "", fmt.Errorf("invalid Windows ISO source")
	}
	allowed, err := filepath.EvalSymlinks(allowedRoot)
	if err != nil {
		return "", err
	}
	storage, err := filepath.EvalSymlinks(req.StorageRoot)
	if err != nil {
		return "", err
	}
	relStorage, err := filepath.Rel(allowed, storage)
	if err != nil || relStorage == "." || relStorage == ".." || strings.HasPrefix(relStorage, ".."+string(filepath.Separator)) || strings.ContainsRune(relStorage, filepath.Separator) {
		if !insideLocalRoot(storage, localRoot) {
			return "", fmt.Errorf("ISO storage is outside the managed SMB and local roots")
		}
	}
	components := strings.Split(req.RelativePath, "/")
	candidate := storage
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("invalid ISO path component")
		}
		candidate = filepath.Join(candidate, component)
		info, err := os.Lstat(candidate)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("ISO source path contains a symlink")
		}
	}
	info, err := os.Stat(candidate)
	// PostgreSQL stores the catalog timestamp to microsecond precision.
	if err != nil || !info.Mode().IsRegular() || info.Size() != req.Size || !info.ModTime().Truncate(time.Microsecond).Equal(req.Modified.Truncate(time.Microsecond)) {
		return "", fmt.Errorf("ISO source changed or is unavailable")
	}
	return candidate, nil
}

func insideLocalRoot(storage string, roots []string) bool {
	for _, root := range roots {
		if root == "" {
			continue
		}
		local, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(local, storage)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

func inspectMountedWindowsISO(root string) (string, error) {
	for _, name := range []string{"efi/boot/bootx64.efi", "sources/boot.wim", "sources/setup.exe"} {
		if err := requireRegularISOFile(root, name); err != nil {
			return "", err
		}
	}
	for _, name := range []string{"sources/install.wim", "sources/install.esd"} {
		if err := requireRegularISOFile(root, name); err == nil {
			return path.Base(name), nil
		}
	}
	return "", fmt.Errorf("Windows installation image is missing")
}

func requireRegularISOFile(root, name string) error {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("required Windows ISO file is missing: %s", name)
	}
	return nil
}

func prepareWindowsISOAssets(mountedRoot, assetRoot string, profileID int64, template, winpeshl []byte, password string) (string, error) {
	if profileID <= 0 {
		return "", fmt.Errorf("invalid Windows ISO profile ID")
	}
	installFile, err := inspectMountedWindowsISO(mountedRoot)
	if err != nil {
		return "", err
	}
	script, err := winpe.RenderProfileStartup(template, password, profileID, installFile)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(assetRoot, 0755); err != nil {
		return "", err
	}
	final := filepath.Join(assetRoot, fmt.Sprintf("%d", profileID))
	if info, err := os.Lstat(final); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("Windows ISO asset target is not a directory")
		}
		for _, name := range []string{"boot.wim", "winpeshl.ini", "pxe-setup.cmd"} {
			if err := requireRegularISOFile(final, name); err != nil {
				return "", err
			}
		}
		if err := os.Chmod(final, 0755); err != nil {
			return "", err
		}
		return installFile, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	stage, err := os.MkdirTemp(assetRoot, ".winiso-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	source, err := os.Open(filepath.Join(mountedRoot, "sources", "boot.wim"))
	if err != nil {
		return "", err
	}
	defer source.Close()
	boot, err := os.OpenFile(filepath.Join(stage, "boot.wim"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(boot, source); err != nil {
		boot.Close()
		return "", err
	}
	if err := boot.Close(); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(stage, "winpeshl.ini"), winpeshl, 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(stage, "pxe-setup.cmd"), script, 0644); err != nil {
		return "", err
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return "", err
	}
	if err := os.Rename(stage, final); err != nil {
		return "", err
	}
	return installFile, nil
}
