package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"omb/internal/winpe"
)

const (
	ISOStrelecMode           = "strelec"
	CustomWinPETemplatePath  = "/etc/omb/windows-profiles/omb-setup.cmd.in"
	DefaultWinPETemplatePath = "/usr/share/omb/windows-profiles/omb-setup.cmd.in"
)

// ResolveWinPETemplate reads the customized WinPE startup template if present,
// falling back to the read-only system default.
func ResolveWinPETemplate(customPath, fallbackPath string) ([]byte, error) {
	if info, err := os.Lstat(customPath); err == nil {
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, fmt.Errorf("custom WinPE template is not a regular non-empty file")
		}
		return os.ReadFile(customPath)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	info, err := os.Lstat(fallbackPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("default WinPE template is not a regular non-empty file")
	}
	return os.ReadFile(fallbackPath)
}

type WindowsISORequest struct {
	BootMode     string    `json:"boot_mode,omitempty"`
	ID           int64     `json:"id"`
	StorageRoot  string    `json:"storage_root"`
	RelativePath string    `json:"relative_path"`
	Size         int64     `json:"size"`
	Modified     time.Time `json:"modified"`
}

type WindowsISOResult struct {
	InstallFile string         `json:"install_file"`
	WIMInfo     WindowsWIMInfo `json:"wim_info,omitempty"`
	BootWIMInfo WindowsWIMInfo `json:"boot_wim_info,omitempty"`
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
	SourceRoot  string
	SourceRoots []string
	LocalRoot   string
	MountRoot   string
	AssetRoot   string
	Template    []byte
	Winpeshl    []byte
	Password    string
	Mounter     WindowsISOMounter
	WIMCommands DriverCommandRunner
}

func (r WindowsISORuntime) allowedRoots() []string {
	if len(r.SourceRoots) > 0 {
		return r.SourceRoots
	}
	if r.SourceRoot != "" {
		return []string{r.SourceRoot}
	}
	return nil
}

func (runtime WindowsISORuntime) Prepare(ctx context.Context, req WindowsISORequest) (WindowsISOResult, error) {
	source, err := resolveWindowsISOSourceRoots(runtime.allowedRoots(), req, runtime.LocalRoot)
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
	var installFile string
	var wimInfo WindowsWIMInfo
	var bootWIMInfo WindowsWIMInfo
	if req.BootMode == ISOStrelecMode {
		err = prepareStrelecISOAssets(ctx, target, runtime.AssetRoot, req.ID, runtime.Template, runtime.Password, runtime.WIMCommands)
	} else {
		installFile, err = prepareWindowsISOAssets(target, runtime.AssetRoot, req.ID, runtime.Template, runtime.Winpeshl, runtime.Password)
		if err == nil {
			info, wimErr := inspectWindowsWIMInfo(ctx, runtime.WIMCommands, filepath.Join(target, "sources", installFile))
			if wimErr != nil {
				log.Printf("windows-iso %d: wim info unavailable (%v); continuing with wim_info.known=false", req.ID, wimErr)
			} else {
				wimInfo = info
			}
			bootInfo, bootWIMErr := inspectWindowsWIMInfo(ctx, runtime.WIMCommands, filepath.Join(runtime.AssetRoot, strconv.FormatInt(req.ID, 10), "boot.wim"))
			if bootWIMErr != nil {
				log.Printf("windows-iso %d: boot.wim info unavailable (%v); continuing with boot_wim_info.known=false", req.ID, bootWIMErr)
			} else {
				bootWIMInfo = bootInfo
			}
		}
	}
	if err != nil {
		if newMount {
			_ = runtime.Mounter.Unmount(ctx, target)
		}
		return WindowsISOResult{}, err
	}
	return WindowsISOResult{InstallFile: installFile, WIMInfo: wimInfo, BootWIMInfo: bootWIMInfo}, nil
}

// LocalISORoot holds the local ISO storages of this server; an ISO for a
// Windows profile comes either from an SMB or NFS storage or from under this root.
const LocalISORoot = "/var/lib/omb-images"

// resolveWindowsISOSourceRoots finds the ISO of a profile request. Its storage
// must be one mount directly under one of allowedRoots, or localRoot or a
// directory inside it.
func resolveWindowsISOSourceRoots(allowedRoots []string, req WindowsISORequest, localRoot ...string) (string, error) {
	if req.BootMode != "" && req.BootMode != ISOStrelecMode {
		return "", fmt.Errorf("invalid Windows ISO source")
	}
	return resolveWindowsSourceFile(allowedRoots, req.ID, req.Size, req.Modified, req.StorageRoot, req.RelativePath, []string{".iso"}, localRoot...)
}

// resolveWindowsSourceFile validates and resolves an ISO/WIM/ESD source file
// inside allowedRoots or localRoot, checking extension, symlinks, size, and mtime.
func resolveWindowsSourceFile(allowedRoots []string, id, size int64, modified time.Time, storageRoot, relativePath string, allowedExts []string, localRoot ...string) (string, error) {
	if id <= 0 || size <= 0 || !filepath.IsAbs(storageRoot) || relativePath == "" || path.IsAbs(relativePath) || path.Clean(relativePath) != relativePath || strings.ContainsAny(relativePath, "\\\x00") || !hasAllowedExt(relativePath, allowedExts) {
		return "", fmt.Errorf("invalid Windows image source")
	}
	storage, err := filepath.EvalSymlinks(storageRoot)
	if err != nil {
		return "", err
	}
	inAllowedRoot := false
	for _, root := range allowedRoots {
		if root == "" {
			continue
		}
		allowed, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		relStorage, err := filepath.Rel(allowed, storage)
		if err != nil || relStorage == "." || relStorage == ".." || strings.HasPrefix(relStorage, ".."+string(filepath.Separator)) || strings.ContainsRune(relStorage, filepath.Separator) {
			continue
		}
		inAllowedRoot = true
		break
	}
	if !inAllowedRoot {
		if !insideLocalRoot(storage, localRoot) {
			return "", fmt.Errorf("image storage is outside the managed SMB, NFS, and local roots")
		}
	}
	components := strings.Split(relativePath, "/")
	candidate := storage
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("invalid image path component")
		}
		candidate = filepath.Join(candidate, component)
		info, err := os.Lstat(candidate)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("image source path contains a symlink")
		}
	}
	info, err := os.Stat(candidate)
	// PostgreSQL stores the catalog timestamp to microsecond precision.
	if err != nil || !info.Mode().IsRegular() || info.Size() != size || !info.ModTime().Truncate(time.Microsecond).Equal(modified.Truncate(time.Microsecond)) {
		return "", fmt.Errorf("image source changed or is unavailable")
	}
	return candidate, nil
}

func hasAllowedExt(name string, exts []string) bool {
	ext := path.Ext(name)
	for _, allowed := range exts {
		if strings.EqualFold(ext, allowed) {
			return true
		}
	}
	return false
}

// resolveWindowsISOSource preserves backward compatibility for callers with a single root.
func resolveWindowsISOSource(allowedRoot string, req WindowsISORequest, localRoot ...string) (string, error) {
	var roots []string
	if allowedRoot != "" {
		roots = []string{allowedRoot}
	}
	return resolveWindowsISOSourceRoots(roots, req, localRoot...)
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
		} else if !errors.Is(err, errUnsupportedISO) {
			return "", err
		}
	}
	return "", unsupportedISO("Windows installation image is missing")
}

func requireRegularISOFile(root, name string) error {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return isoPathError(err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return unsupportedISO("required Windows ISO file is missing: %s", name)
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
	answerScript, err := winpe.RenderAnswerStartup(script, profileID)
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
		for _, name := range []string{"boot.wim", "winpeshl.ini", "omb-setup.cmd"} {
			if err := requireRegularISOFile(final, name); err != nil {
				return "", err
			}
		}
		if err := ensureAnswerStartupAsset(final, profileID, answerScript); err != nil {
			return "", err
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
	if err := os.WriteFile(filepath.Join(stage, "omb-setup.cmd"), script, 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(stage, "omb-answer-setup.cmd"), answerScript, 0644); err != nil {
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

// ensureAnswerStartupAsset makes sure omb-answer-setup.cmd exists in an already
// prepared Windows profile directory, generating it atomically from omb-setup.cmd
// (or preRendered when supplied) without touching boot.wim.
func ensureAnswerStartupAsset(profileDir string, profileID int64, preRendered []byte) error {
	target := filepath.Join(profileDir, "omb-answer-setup.cmd")
	info, err := os.Lstat(target)
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return unsupportedISO("required Windows ISO file is missing: omb-answer-setup.cmd")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return isoPathError(err)
	}
	content := preRendered
	if len(content) == 0 {
		if err := requireRegularISOFile(profileDir, "omb-setup.cmd"); err != nil {
			return err
		}
		base, err := os.ReadFile(filepath.Join(profileDir, "omb-setup.cmd"))
		if err != nil {
			return err
		}
		content, err = winpe.RenderAnswerStartup(base, profileID)
		if err != nil {
			return err
		}
	}
	return writeAtomic(target, content, 0644)
}
