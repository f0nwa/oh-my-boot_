package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"omb/internal/autoinstall"
	"omb/internal/boot"
)

type UbuntuISORequest struct {
	WindowsISORequest
	SHA256 string `json:"sha256"`
}

type UbuntuISOAPI interface {
	Prepare(context.Context, UbuntuISORequest) error
	Inspect(context.Context, WindowsISORequest) (UbuntuISOInspection, error)
}

type UbuntuISOManager struct {
	mu               sync.Mutex
	SourceRoot       string
	LocalRoot        string
	AssetRoot        string
	ScriptPath       string
	Runner           DriverCommandRunner
	RegistryRoot     string
	LegacyConfigPath string
	Mounter          WindowsISOMounter
}

type UbuntuISOInspection struct {
	SHA256  string                     `json:"sha256"`
	Sources []autoinstall.SourceOption `json:"sources"`
}

type ISOInspection struct {
	Kind   string              `json:"kind"`
	Ubuntu UbuntuISOInspection `json:"ubuntu"`
}

func (m *UbuntuISOManager) Inspect(ctx context.Context, req WindowsISORequest) (UbuntuISOInspection, error) {
	result, err := m.inspect(ctx, req, false)
	return result.Ubuntu, err
}

func (m *UbuntuISOManager) InspectISO(ctx context.Context, req WindowsISORequest) (ISOInspection, error) {
	return m.inspect(ctx, req, true)
}

func (m *UbuntuISOManager) inspect(ctx context.Context, req WindowsISORequest, detect bool) (result ISOInspection, resultErr error) {
	source, err := resolveWindowsISOSource(m.SourceRoot, req, m.LocalRoot)
	if err != nil {
		return result, err
	}
	before, err := os.Lstat(source)
	if err != nil || !before.Mode().IsRegular() {
		return result, fmt.Errorf("Ubuntu ISO is unavailable")
	}
	iso, err := os.Open(source)
	if err != nil {
		return result, err
	}
	defer iso.Close()
	opened, err := iso.Stat()
	if err != nil || !os.SameFile(before, opened) || opened.Size() != req.Size || !opened.ModTime().Truncate(time.Microsecond).Equal(req.Modified.Truncate(time.Microsecond)) {
		return result, fmt.Errorf("Ubuntu ISO changed before inspection")
	}
	mounter := m.Mounter
	if mounter == nil {
		mounter = CommandWindowsISOMounter{}
	}
	mountRoot, err := os.MkdirTemp("", "omb-ubuntu-inspect-")
	if err != nil {
		return result, err
	}
	mounted := false
	defer func() {
		if mounted {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := mounter.Unmount(cleanupCtx, mountRoot); err != nil {
				resultErr = fmt.Errorf("failed to unmount temporary Ubuntu ISO at %s: %w", mountRoot, err)
				return
			}
		}
		if err := os.RemoveAll(mountRoot); err != nil && resultErr == nil {
			resultErr = err
		}
	}()
	// The parent retains this open descriptor while mount runs, preventing a
	// path replacement between validation and the read-only loop mount.
	mountSource := fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), iso.Fd())
	if err := mounter.Mount(ctx, mountSource, mountRoot); err != nil {
		return result, err
	}
	mounted = true
	defer func() {
		if resultErr != nil {
			return
		}
		after, err := iso.Stat()
		current, pathErr := os.Lstat(source)
		if err != nil || pathErr != nil || !current.Mode().IsRegular() || !os.SameFile(opened, after) || !os.SameFile(opened, current) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) || opened.Size() != current.Size() || !opened.ModTime().Equal(current.ModTime()) {
			resultErr = fmt.Errorf("ISO changed during inspection")
		}
	}()
	if detect {
		if _, err := inspectMountedWindowsISO(mountRoot); err == nil {
			result.Kind = "windows"
			return result, nil
		} else if !errors.Is(err, errUnsupportedISO) {
			return result, err
		}
		if _, err := inspectMountedStrelecISO(mountRoot); err == nil {
			result.Kind = "strelec"
			return result, nil
		} else if !errors.Is(err, errUnsupportedISO) {
			return result, err
		}
	}
	sources, err := inspectMountedUbuntuISO(mountRoot)
	if err != nil {
		if detect && errors.Is(err, errUnsupportedISO) {
			result.Kind = "unsupported"
			return result, nil
		}
		return result, err
	}
	result.Ubuntu.Sources = sources
	hash := sha256.New()
	if _, err := io.Copy(hash, ubuntuContextReader{ctx: ctx, reader: iso}); err != nil {
		return result, err
	}
	result.Kind = "ubuntu"
	result.Ubuntu.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

func inspectMountedUbuntuISO(mountRoot string) ([]autoinstall.SourceOption, error) {
	for _, directory := range []string{".disk", "casper", "EFI", "EFI/boot"} {
		info, err := os.Lstat(filepath.Join(mountRoot, directory))
		if err != nil {
			return nil, isoPathError(err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, unsupportedISO("invalid Ubuntu ISO directory")
		}
	}
	infoData, err := readUbuntuISOFile(mountRoot, ".disk/info", 4096)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(string(infoData), "Ubuntu ") {
		return nil, unsupportedISO("unsupported Ubuntu distribution")
	}
	amd64 := false
	for _, word := range strings.Fields(string(infoData)) {
		if word == "amd64" {
			amd64 = true
		}
	}
	if !amd64 {
		return nil, unsupportedISO("unsupported Ubuntu architecture")
	}
	for _, name := range []string{"casper/vmlinuz", "casper/initrd", "EFI/boot/bootx64.efi"} {
		info, err := os.Lstat(filepath.Join(mountRoot, name))
		if err != nil {
			return nil, isoPathError(err)
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 {
			return nil, unsupportedISO("required Ubuntu ISO file is missing: %s", name)
		}
	}
	data, err := readUbuntuISOFile(mountRoot, "casper/install-sources.yaml", 64*1024)
	if err != nil {
		return nil, err
	}
	sources, err := autoinstall.ParseInstallSources(data)
	if err != nil {
		return nil, unsupportedISO("unsupported Ubuntu Desktop installer: %v", err)
	}
	return sources, nil
}

func readUbuntuISOFile(root, name string, limit int64) ([]byte, error) {
	filePath := filepath.Join(root, name)
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, isoPathError(err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, unsupportedISO("invalid Ubuntu ISO metadata: %s", name)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("Ubuntu ISO metadata changed: %s", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, unsupportedISO("invalid Ubuntu ISO metadata: %s", name)
	}
	return data, nil
}

func (m *UbuntuISOManager) Prepare(ctx context.Context, req UbuntuISORequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Runner == nil || !filepath.IsAbs(m.AssetRoot) || !filepath.IsAbs(m.ScriptPath) || !filepath.IsAbs(m.RegistryRoot) {
		return fmt.Errorf("Ubuntu ISO preparation unavailable")
	}
	source, err := m.Validate(ctx, req)
	if err != nil {
		return err
	}
	id := strconv.FormatInt(req.ID, 10)
	if _, err := m.Runner.Run(ctx, "", m.ScriptPath, "--iso", source, "--assets", m.AssetRoot, "--source-id", id, "--sha256", req.SHA256); err != nil {
		return err
	}
	prepared := boot.NewUbuntuSourceAt(source, filepath.Join(m.AssetRoot, "ubuntu-sources", id, req.SHA256))
	catalog, err := prepared.Catalog()
	if err != nil || catalog.ISOSHA256 != req.SHA256 {
		return fmt.Errorf("prepared Ubuntu source is invalid")
	}
	if err := os.MkdirAll(m.RegistryRoot, 0755); err != nil {
		return err
	}
	info, err := os.Lstat(m.RegistryRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || !agentOwnsFile(info) {
		return fmt.Errorf("Ubuntu source registry is unavailable")
	}
	contents, err := json.Marshal(struct {
		ID      int64  `json:"id"`
		SHA256  string `json:"sha256"`
		ISOPath string `json:"iso_path"`
	}{req.ID, req.SHA256, source})
	if err != nil {
		return err
	}
	configPath := filepath.Join(m.RegistryRoot, id+".json")
	if oldInfo, err := os.Lstat(configPath); err == nil {
		if !oldInfo.Mode().IsRegular() || oldInfo.Size() > 4096 || oldInfo.Mode().Perm()&0022 != 0 || !agentOwnsFile(oldInfo) {
			return fmt.Errorf("invalid existing Ubuntu source registry entry")
		}
		existing, err := os.ReadFile(configPath)
		if err != nil || string(existing) != string(contents) {
			return fmt.Errorf("Ubuntu source registry entry is immutable")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeAtomic(configPath, contents, 0644)
}

func (m *UbuntuISOManager) Validate(ctx context.Context, req UbuntuISORequest) (string, error) {
	if len(req.SHA256) != 64 || !validManagedChild(req.SHA256) {
		return "", fmt.Errorf("invalid Ubuntu ISO digest")
	}
	source, err := resolveWindowsISOSource(m.SourceRoot, req.WindowsISORequest, m.LocalRoot)
	if err != nil {
		return "", err
	}
	before, err := os.Lstat(source)
	if err != nil || !before.Mode().IsRegular() {
		return "", fmt.Errorf("Ubuntu ISO is unavailable")
	}
	file, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", fmt.Errorf("Ubuntu ISO changed before verification")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, ubuntuContextReader{ctx: ctx, reader: file}); err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return "", fmt.Errorf("Ubuntu ISO changed during verification")
	}
	current, err := os.Lstat(source)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) || current.Size() != opened.Size() || !current.ModTime().Equal(opened.ModTime()) {
		return "", fmt.Errorf("Ubuntu ISO changed after verification")
	}
	if hex.EncodeToString(hash.Sum(nil)) != req.SHA256 {
		return "", fmt.Errorf("Ubuntu ISO digest does not match")
	}
	return source, nil
}

type ubuntuContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r ubuntuContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// HandlerWithUbuntuISO exposes preparation only over the agent's local socket.
func HandlerWithUbuntuISO(next http.Handler, manager UbuntuISOAPI) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ubuntu-iso/pin-legacy", func(w http.ResponseWriter, r *http.Request) {
		pinner, ok := manager.(interface {
			PinLegacy(context.Context, int64, string) error
		})
		if !ok || pinner == nil {
			http.Error(w, "legacy Ubuntu source pinning unavailable", http.StatusServiceUnavailable)
			return
		}
		var req ubuntuLegacyRef
		if !decodeSMBRequest(w, r, &req) {
			return
		}
		if req.ID <= 0 || !validUbuntuDigest(req.SHA256) {
			http.Error(w, "invalid legacy Ubuntu source", http.StatusBadRequest)
			return
		}
		if err := pinner.PinLegacy(r.Context(), req.ID, req.SHA256); err != nil {
			http.Error(w, "legacy Ubuntu source verification failed", http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /iso/inspect", func(w http.ResponseWriter, r *http.Request) {
		detector, ok := manager.(interface {
			InspectISO(context.Context, WindowsISORequest) (ISOInspection, error)
		})
		if !ok {
			http.Error(w, "ISO inspection unavailable", http.StatusServiceUnavailable)
			return
		}
		var req WindowsISORequest
		if !decodeSMBRequest(w, r, &req) {
			return
		}
		if !validUbuntuISORequest(req) {
			http.Error(w, "invalid ISO source", http.StatusBadRequest)
			return
		}
		result, err := detector.InspectISO(r.Context(), req)
		if err != nil {
			http.Error(w, "ISO inspection failed", http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /ubuntu-iso/inspect", func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			http.Error(w, "Ubuntu ISO inspection unavailable", http.StatusServiceUnavailable)
			return
		}
		var req WindowsISORequest
		if !decodeSMBRequest(w, r, &req) {
			return
		}
		if !validUbuntuISORequest(req) {
			http.Error(w, "invalid Ubuntu ISO source", http.StatusBadRequest)
			return
		}
		result, err := manager.Inspect(r.Context(), req)
		if err != nil {
			http.Error(w, "Ubuntu ISO inspection failed", http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /ubuntu-iso/prepare", func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			http.Error(w, "Ubuntu ISO preparation unavailable", http.StatusServiceUnavailable)
			return
		}
		var req UbuntuISORequest
		if !decodeSMBRequest(w, r, &req) {
			return
		}
		if !validUbuntuISORequest(req.WindowsISORequest) || len(req.SHA256) != 64 || !validManagedChild(req.SHA256) {
			http.Error(w, "invalid Ubuntu ISO source", http.StatusBadRequest)
			return
		}
		if err := manager.Prepare(r.Context(), req); err != nil {
			http.Error(w, "Ubuntu ISO preparation failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", next)
	return mux
}

func validUbuntuISORequest(req WindowsISORequest) bool {
	return req.ID > 0 && req.BootMode == "" && req.Size > 0 && !req.Modified.IsZero() && filepath.IsAbs(req.StorageRoot) && req.RelativePath != "" && !path.IsAbs(req.RelativePath) && path.Clean(req.RelativePath) == req.RelativePath && req.RelativePath != ".." && !strings.HasPrefix(req.RelativePath, "../") && !strings.ContainsAny(req.RelativePath, "\\\x00") && strings.EqualFold(path.Ext(req.RelativePath), ".iso")
}
