package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidWIMType indicates that a WIM/ESD file does not match the expected
// role (for example, an install.wim uploaded as boot.wim or a WinPE boot.wim
// uploaded as install.wim).
var ErrInvalidWIMType = errors.New("invalid Windows WIM image type")

// WindowsBootImageRequest identifies a standalone boot.wim file in a managed storage.
type WindowsBootImageRequest struct {
	ID           int64     `json:"id"`
	StorageRoot  string    `json:"storage_root"`
	RelativePath string    `json:"relative_path"`
	Size         int64     `json:"size"`
	Modified     time.Time `json:"modified"`
}

// WindowsBootImageResult reports verified metadata of a prepared standalone boot.wim.
type WindowsBootImageResult struct {
	SHA256      string         `json:"sha256"`
	HasSetupExe bool           `json:"has_setup_exe"`
	WIMInfo     WindowsWIMInfo `json:"wim_info"`
}

// WindowsInstallImageRequest identifies a standalone install.wim or install.esd file.
type WindowsInstallImageRequest struct {
	ID           int64     `json:"id"`
	StorageRoot  string    `json:"storage_root"`
	RelativePath string    `json:"relative_path"`
	Size         int64     `json:"size"`
	Modified     time.Time `json:"modified"`
}

// WindowsInstallImageResult reports verified metadata of a prepared standalone install.wim/esd.
type WindowsInstallImageResult struct {
	InstallFile string         `json:"install_file"`
	WIMInfo     WindowsWIMInfo `json:"wim_info"`
}

// InstallImageMounter manages read-only file bind mounts for standalone install.wim/esd images.
type InstallImageMounter interface {
	IsMounted(ctx context.Context, target string) (bool, error)
	BindMountRO(ctx context.Context, source, target string) error
	Unmount(ctx context.Context, target string) error
}

// CommandInstallImageMounter implements InstallImageMounter via mount/umount/findmnt.
type CommandInstallImageMounter struct{}

func (CommandInstallImageMounter) IsMounted(ctx context.Context, target string) (bool, error) {
	err := exec.CommandContext(ctx, "findmnt", "--mountpoint", target).Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func (CommandInstallImageMounter) BindMountRO(ctx context.Context, source, target string) error {
	if err := run(ctx, "mount", "--bind", source, target); err != nil {
		return err
	}
	if err := run(ctx, "mount", "-o", "remount,ro,bind,nosuid,nodev,noexec", target); err != nil {
		_ = run(ctx, "umount", target)
		return err
	}
	return nil
}

func (CommandInstallImageMounter) Unmount(ctx context.Context, target string) error {
	return run(ctx, "umount", target)
}

type bootImageMeta struct {
	Request WindowsBootImageRequest `json:"request"`
	Result  WindowsBootImageResult  `json:"result"`
}

type storedInstallImage struct {
	Request     WindowsInstallImageRequest `json:"request"`
	InstallFile string                     `json:"install_file"`
}

func (m *WindowsISOManager) bootImageRoot() (string, error) {
	if m.BootImageRoot != "" {
		return m.BootImageRoot, nil
	}
	if m.AssetRoot != "" {
		return filepath.Join(filepath.Dir(m.AssetRoot), "windows-boot-images"), nil
	}
	return "", fmt.Errorf("Windows boot image root is unavailable")
}

func (m *WindowsISOManager) installConfigRoot() (string, error) {
	if m.InstallConfigRoot != "" {
		return m.InstallConfigRoot, nil
	}
	if m.ConfigRoot != "" {
		return filepath.Join(m.ConfigRoot, "install-images"), nil
	}
	return "", fmt.Errorf("Windows install image config root is unavailable")
}

func (m *WindowsISOManager) installMountRoot() (string, error) {
	if m.InstallMountRoot != "" {
		return m.InstallMountRoot, nil
	}
	if m.MountRoot != "" {
		return m.MountRoot, nil
	}
	return "", fmt.Errorf("Windows install image mount root is unavailable")
}

func installImageDirName(id int64) string {
	return "install-" + strconv.FormatInt(id, 10)
}

func (m *WindowsISOManager) installMounter() InstallImageMounter {
	if m.InstallMounter != nil {
		return m.InstallMounter
	}
	return CommandInstallImageMounter{}
}

func (m *WindowsISOManager) wimRunner() DriverCommandRunner {
	if m.WIMCommands != nil {
		return m.WIMCommands
	}
	return ExecDriverCommandRunner{}
}

// PrepareBootImage verifies a standalone boot.wim, inspects its boot index and
// presence of /sources/setup.exe, computes its SHA-256, and caches it under
// <BootImageRoot>/<id>/boot.wim.
func (m *WindowsISOManager) PrepareBootImage(ctx context.Context, req WindowsBootImageRequest) (WindowsBootImageResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	source, err := resolveWindowsSourceFile(m.allowedRoots(), req.ID, req.Size, req.Modified, req.StorageRoot, req.RelativePath, []string{".wim"}, m.LocalRoot)
	if err != nil {
		return WindowsBootImageResult{}, err
	}
	bootRoot, err := m.bootImageRoot()
	if err != nil {
		return WindowsBootImageResult{}, err
	}
	if err := os.MkdirAll(bootRoot, 0o755); err != nil {
		return WindowsBootImageResult{}, err
	}
	idStr := strconv.FormatInt(req.ID, 10)
	final := filepath.Join(bootRoot, idStr)
	if cached, ok := readCachedBootImage(final, req); ok {
		return cached, nil
	}

	stage, err := os.MkdirTemp(bootRoot, ".boot-image-")
	if err != nil {
		return WindowsBootImageResult{}, err
	}
	defer os.RemoveAll(stage)

	copyPath := filepath.Join(stage, "boot.wim")
	if err := copyRegularFile(source, copyPath, 0o644); err != nil {
		return WindowsBootImageResult{}, err
	}

	runner := m.wimRunner()
	infoOut, err := runner.Run(ctx, "", "wimlib-imagex", "info", copyPath)
	if err != nil {
		return WindowsBootImageResult{}, err
	}
	bootIndex := parseBootIndex(infoOut)
	if bootIndex <= 0 {
		return WindowsBootImageResult{}, fmt.Errorf("%w: file is not a bootable WinPE WIM (Boot Index is 0)", ErrInvalidWIMType)
	}
	wimInfo, err := ParseWIMInfo(infoOut)
	if err != nil {
		return WindowsBootImageResult{}, err
	}

	hasSetupExe := false
	dirOut, dirErr := runner.Run(ctx, "", "wimlib-imagex", "dir", copyPath, strconv.Itoa(bootIndex), "--path=/sources")
	if dirErr == nil {
		hasSetupExe = hasSetupExeInDir(dirOut)
	}

	if _, err := runner.Run(ctx, "", "wimlib-imagex", "verify", copyPath); err != nil {
		return WindowsBootImageResult{}, err
	}

	sha, _, err := hashFile(copyPath)
	if err != nil {
		return WindowsBootImageResult{}, err
	}
	result := WindowsBootImageResult{
		SHA256:      sha,
		HasSetupExe: hasSetupExe,
		WIMInfo:     wimInfo,
	}
	storedReq := req
	storedReq.Modified = req.Modified.UTC()
	metaBytes, err := json.Marshal(bootImageMeta{Request: storedReq, Result: result})
	if err != nil {
		return WindowsBootImageResult{}, err
	}
	if err := os.WriteFile(filepath.Join(stage, "meta.json"), metaBytes, 0o644); err != nil {
		return WindowsBootImageResult{}, err
	}
	if err := os.Chmod(stage, 0o755); err != nil {
		return WindowsBootImageResult{}, err
	}

	if info, err := os.Lstat(final); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return WindowsBootImageResult{}, fmt.Errorf("boot image target is not a plain directory")
		}
		if err := os.RemoveAll(final); err != nil {
			return WindowsBootImageResult{}, err
		}
	} else if !os.IsNotExist(err) {
		return WindowsBootImageResult{}, err
	}
	if err := os.Rename(stage, final); err != nil {
		return WindowsBootImageResult{}, err
	}
	return result, nil
}

// RemoveBootImage removes the cached standalone boot.wim directory for id.
func (m *WindowsISOManager) RemoveBootImage(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id <= 0 {
		return fmt.Errorf("invalid Windows boot image ID")
	}
	bootRoot, err := m.bootImageRoot()
	if err != nil {
		return err
	}
	if _, err := os.Lstat(bootRoot); os.IsNotExist(err) {
		return nil
	}
	return removeManagedDirectory(bootRoot, strconv.FormatInt(id, 10))
}

// PrepareInstallImage validates a standalone install.wim or install.esd,
// rejects pure WinPE images, and publishes the file read-only via bind mount
// under <InstallMountRoot>/install-<id>/sources/install.{wim,esd}.
func (m *WindowsISOManager) PrepareInstallImage(ctx context.Context, req WindowsInstallImageRequest) (WindowsInstallImageResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	source, err := resolveWindowsSourceFile(m.allowedRoots(), req.ID, req.Size, req.Modified, req.StorageRoot, req.RelativePath, []string{".wim", ".esd"}, m.LocalRoot)
	if err != nil {
		return WindowsInstallImageResult{}, err
	}
	installFile := "install.wim"
	if strings.EqualFold(path.Ext(req.RelativePath), ".esd") {
		installFile = "install.esd"
	}

	runner := m.wimRunner()
	infoOut, err := runner.Run(ctx, "", "wimlib-imagex", "info", source)
	if err != nil {
		return WindowsInstallImageResult{}, err
	}
	if bootIndex := parseBootIndex(infoOut); bootIndex > 0 {
		return WindowsInstallImageResult{}, fmt.Errorf("%w: file is a bootable WinPE image (Boot Index: %d), not an installation image", ErrInvalidWIMType, bootIndex)
	}
	wimInfo, err := ParseWIMInfo(infoOut)
	if err != nil {
		return WindowsInstallImageResult{}, err
	}
	if isPureWinPE(infoOut, wimInfo) {
		return WindowsInstallImageResult{}, fmt.Errorf("%w: file contains only WinPE images", ErrInvalidWIMType)
	}

	mountRoot, err := m.installMountRoot()
	if err != nil {
		return WindowsInstallImageResult{}, err
	}
	configRoot, err := m.installConfigRoot()
	if err != nil {
		return WindowsInstallImageResult{}, err
	}
	target, newMount, err := m.mountInstallImageFile(ctx, mountRoot, req.ID, installFile, source)
	if err != nil {
		return WindowsInstallImageResult{}, err
	}

	if err := os.MkdirAll(configRoot, 0o700); err != nil {
		if newMount {
			_ = m.installMounter().Unmount(ctx, target)
		}
		return WindowsInstallImageResult{}, err
	}
	storedReq := req
	storedReq.Modified = req.Modified.UTC()
	content, err := json.Marshal(storedInstallImage{Request: storedReq, InstallFile: installFile})
	if err != nil {
		if newMount {
			_ = m.installMounter().Unmount(ctx, target)
		}
		return WindowsInstallImageResult{}, err
	}
	configPath := filepath.Join(configRoot, strconv.FormatInt(req.ID, 10)+".json")
	if err := writeAtomic(configPath, content, 0o600); err != nil {
		if newMount {
			_ = m.installMounter().Unmount(ctx, target)
		}
		return WindowsInstallImageResult{}, err
	}
	return WindowsInstallImageResult{InstallFile: installFile, WIMInfo: wimInfo}, nil
}

// RemoveInstallImage unmounts and removes the bind-mounted install image and its config.
func (m *WindowsISOManager) RemoveInstallImage(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id <= 0 {
		return fmt.Errorf("invalid Windows install image ID")
	}
	mountRoot, err := m.installMountRoot()
	if err != nil {
		return err
	}
	configRoot, err := m.installConfigRoot()
	if err != nil {
		return err
	}
	dirName := installImageDirName(id)
	targetDir := filepath.Join(mountRoot, dirName)
	if info, err := os.Lstat(targetDir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Windows install image mount target is unsafe")
		}
		mounter := m.installMounter()
		for _, name := range []string{"install.wim", "install.esd"} {
			fileTarget := filepath.Join(targetDir, "sources", name)
			if _, statErr := os.Lstat(fileTarget); statErr == nil {
				mounted, checkErr := mounter.IsMounted(ctx, fileTarget)
				if checkErr != nil {
					return checkErr
				}
				if mounted {
					if err := mounter.Unmount(ctx, fileTarget); err != nil {
						return err
					}
				}
			} else if !os.IsNotExist(statErr) {
				return statErr
			}
		}
		if err := requirePlainDirectory(mountRoot); err != nil {
			return err
		}
		if err := os.RemoveAll(targetDir); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Lstat(configRoot); err == nil {
		if err := removeManagedFile(configRoot, strconv.FormatInt(id, 10)); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *WindowsISOManager) mountInstallImageFile(ctx context.Context, mountRoot string, id int64, installFile, source string) (string, bool, error) {
	if err := os.MkdirAll(mountRoot, 0o755); err != nil {
		return "", false, err
	}
	dir := filepath.Join(mountRoot, installImageDirName(id))
	sourcesDir := filepath.Join(dir, "sources")
	if err := ensurePlainDir(dir); err != nil {
		return "", false, err
	}
	if err := ensurePlainDir(sourcesDir); err != nil {
		return "", false, err
	}
	target := filepath.Join(sourcesDir, installFile)
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", false, fmt.Errorf("install image mount target is not a regular file")
		}
	} else if os.IsNotExist(err) {
		f, createErr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if createErr != nil {
			return "", false, createErr
		}
		if closeErr := f.Close(); closeErr != nil {
			return "", false, closeErr
		}
	} else {
		return "", false, err
	}

	mounter := m.installMounter()
	mounted, err := mounter.IsMounted(ctx, target)
	if err != nil {
		return "", false, err
	}
	if mounted {
		return target, false, nil
	}
	if err := mounter.BindMountRO(ctx, source, target); err != nil {
		return "", false, err
	}
	return target, true, nil
}

func (m *WindowsISOManager) stopInstallImagesOn(ctx context.Context, roots []string) ([]string, error) {
	configRoot, err := m.installConfigRoot()
	if err != nil {
		return nil, nil
	}
	mountRoot, err := m.installMountRoot()
	if err != nil {
		return nil, nil
	}
	configs, err := filepath.Glob(filepath.Join(configRoot, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(configs)
	mounter := m.installMounter()
	var actions []string
	var problems []error
	for _, cfgPath := range configs {
		id, err := strconv.ParseInt(strings.TrimSuffix(filepath.Base(cfgPath), ".json"), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			continue
		}
		var stored storedInstallImage
		if json.Unmarshal(raw, &stored) != nil || stored.InstallFile == "" {
			continue
		}
		uses := false
		clean := filepath.Clean(stored.Request.StorageRoot)
		for _, root := range roots {
			cleanRoot := filepath.Clean(root)
			uses = uses || clean == cleanRoot || strings.HasPrefix(clean, cleanRoot+string(filepath.Separator))
		}
		if !uses {
			continue
		}
		target := filepath.Join(mountRoot, installImageDirName(id), "sources", stored.InstallFile)
		mounted, err := mounter.IsMounted(ctx, target)
		if err != nil {
			problems = append(problems, fmt.Errorf("install-image %d: %w", id, err))
			continue
		}
		if !mounted {
			continue
		}
		if err := mounter.Unmount(ctx, target); err != nil {
			problems = append(problems, fmt.Errorf("install-image %d: %w", id, err))
			continue
		}
		actions = append(actions, fmt.Sprintf("unmounted install-image %d to release its share", id))
	}
	return actions, errors.Join(problems...)
}

func (m *WindowsISOManager) recoverInstallImages(ctx context.Context) ([]string, error) {
	configRoot, err := m.installConfigRoot()
	if err != nil {
		return nil, nil
	}
	mountRoot, err := m.installMountRoot()
	if err != nil {
		return nil, nil
	}
	configs, err := filepath.Glob(filepath.Join(configRoot, "*.json"))
	if err != nil {
		return nil, err
	}
	type item struct {
		id   int64
		path string
	}
	var items []item
	for _, cfgPath := range configs {
		id, err := strconv.ParseInt(strings.TrimSuffix(filepath.Base(cfgPath), ".json"), 10, 64)
		if err == nil && id > 0 {
			items = append(items, item{id: id, path: cfgPath})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })

	mounter := m.installMounter()
	var actions []string
	var problems []error
	for _, it := range items {
		raw, err := os.ReadFile(it.path)
		if err != nil {
			problems = append(problems, fmt.Errorf("install-image %d: %w", it.id, err))
			continue
		}
		var stored storedInstallImage
		if err := json.Unmarshal(raw, &stored); err != nil || (stored.InstallFile != "install.wim" && stored.InstallFile != "install.esd") {
			problems = append(problems, fmt.Errorf("install-image %d: invalid stored metadata", it.id))
			continue
		}
		target := filepath.Join(mountRoot, installImageDirName(it.id), "sources", stored.InstallFile)
		mounted, err := mounter.IsMounted(ctx, target)
		if err == nil && mounted {
			continue
		}
		source, err := resolveWindowsSourceFile(m.allowedRoots(), stored.Request.ID, stored.Request.Size, stored.Request.Modified, stored.Request.StorageRoot, stored.Request.RelativePath, []string{".wim", ".esd"}, m.LocalRoot)
		if err != nil {
			problems = append(problems, fmt.Errorf("install-image %d: %w", it.id, err))
			continue
		}
		if _, mountedNow, err := m.mountInstallImageFile(ctx, mountRoot, it.id, stored.InstallFile, source); err != nil {
			problems = append(problems, fmt.Errorf("install-image %d: %w", it.id, err))
		} else if mountedNow {
			actions = append(actions, fmt.Sprintf("mounted install-image %d", it.id))
		}
	}
	return actions, errors.Join(problems...)
}

func parseBootIndex(raw []byte) int {
	match := bootIndexLine.FindSubmatch(raw)
	if len(match) != 2 {
		return 0
	}
	idx, err := strconv.Atoi(string(match[1]))
	if err != nil || idx < 0 {
		return 0
	}
	return idx
}

func hasSetupExeInDir(raw []byte) bool {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, line := range strings.Split(text, "\n") {
		norm := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(line, `\`, "/")))
		if norm == "/sources/setup.exe" || norm == "sources/setup.exe" || norm == "setup.exe" {
			return true
		}
	}
	return false
}

// isPureWinPE returns true if all images in the WIM are WinPE/Setup images
// (checked via Flags and image names when Edition ID is absent).
func isPureWinPE(raw []byte, info WindowsWIMInfo) bool {
	if len(info.Images) == 0 {
		return false
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	flagsByIndex := map[int]string{}
	currentIdx := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		key, val, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "Index" {
			if idx, err := strconv.Atoi(val); err == nil && idx > 0 {
				currentIdx = idx
			} else {
				currentIdx = 0
			}
			continue
		}
		if currentIdx > 0 && key == "Flags" {
			flagsByIndex[currentIdx] = val
		}
	}
	for _, img := range info.Images {
		if img.EditionID != "" {
			return false
		}
		flagLower := strings.ToLower(flagsByIndex[img.Index])
		nameLower := strings.ToLower(img.Name)
		isPE := strings.Contains(flagLower, "winpe") || strings.Contains(flagLower, "windowspe") || flagLower == "9" ||
			strings.Contains(nameLower, "windows pe") || strings.Contains(nameLower, "windows setup")
		if !isPE {
			return false
		}
	}
	return true
}

func readCachedBootImage(dir string, req WindowsBootImageRequest) (WindowsBootImageResult, bool) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return WindowsBootImageResult{}, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return WindowsBootImageResult{}, false
	}
	var meta bootImageMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return WindowsBootImageResult{}, false
	}
	if meta.Request.ID != req.ID || meta.Request.StorageRoot != req.StorageRoot || meta.Request.RelativePath != req.RelativePath || meta.Request.Size != req.Size || !meta.Request.Modified.Truncate(time.Microsecond).Equal(req.Modified.Truncate(time.Microsecond)) {
		return WindowsBootImageResult{}, false
	}
	if !validDriverHash(meta.Result.SHA256) {
		return WindowsBootImageResult{}, false
	}
	actual, _, err := hashFile(filepath.Join(dir, "boot.wim"))
	if err != nil || actual != meta.Result.SHA256 {
		return WindowsBootImageResult{}, false
	}
	return meta.Result, true
}

func copyRegularFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		in.Close()
		return err
	}
	_, copyErr := io.Copy(out, in)
	inCloseErr := in.Close()
	outCloseErr := out.Close()
	if copyErr != nil || inCloseErr != nil || outCloseErr != nil {
		return fmt.Errorf("copy WIM file failed")
	}
	return nil
}

func ensurePlainDir(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("target path is not a plain directory: %s", path)
		}
		return nil
	}
	if os.IsNotExist(err) {
		return os.Mkdir(path, 0o755)
	}
	return err
}
