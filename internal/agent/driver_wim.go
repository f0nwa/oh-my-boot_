package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"omb/internal/drivers"
	"omb/internal/winpe"
)

type SelectedINF struct {
	PackageSHA256 string `json:"package_sha256"`
	INFPath       string `json:"inf_path"`
}

type DriverBuildRequest struct {
	ConfigID    int64         `json:"config_id"`
	ProfileID   int64         `json:"profile_id"`
	AnswerID    int64         `json:"answer_id"`
	PackageINFs []SelectedINF `json:"package_infs"`
}

type DriverBuildResult struct {
	WIMKey       string `json:"wim_key"`
	WIMSHA256    string `json:"wim_sha256"`
	ScriptSHA256 string `json:"script_sha256"`
}

type DriverBuildCheck struct {
	Request DriverBuildRequest `json:"request"`
	Result  DriverBuildResult  `json:"result"`
}

type driverConfigManifest struct {
	Request DriverBuildRequest `json:"request"`
	Result  DriverBuildResult  `json:"result"`
}

type DriverCommandRunner interface {
	Run(context.Context, string, string, ...string) ([]byte, error)
}

type ExecDriverCommandRunner struct{}

func (ExecDriverCommandRunner) Run(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s failed: %w: %s", name, err, string(out))
	}
	return out, nil
}

var bootIndexLine = regexp.MustCompile(`(?m)^Boot Index:\s*([0-9]+)\s*$`)

func (m *DriverManager) PrepareConfig(ctx context.Context, req DriverBuildRequest) (DriverBuildResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateBuildRequest(req); err != nil {
		return DriverBuildResult{}, err
	}
	source := filepath.Join(m.ProfileRoot, strconv.FormatInt(req.ProfileID, 10), "boot.wim")
	if err := requireRegularISOFile(filepath.Dir(source), "boot.wim"); err != nil {
		return DriverBuildResult{}, err
	}
	sourceHash, sourceSize, err := hashFile(source)
	if err != nil {
		return DriverBuildResult{}, err
	}
	script, err := m.driverScript(req)
	if err != nil {
		return DriverBuildResult{}, err
	}
	key := driverWIMKey(sourceHash, req.PackageINFs)
	if err := os.MkdirAll(m.WIMRoot, 0755); err != nil {
		return DriverBuildResult{}, err
	}
	if err := os.MkdirAll(m.ConfigRoot, 0755); err != nil {
		return DriverBuildResult{}, err
	}
	wimHash, err := m.ensureDriverWIM(ctx, source, sourceSize, key, req.PackageINFs)
	if err != nil {
		return DriverBuildResult{}, err
	}
	scriptSum := sha256.Sum256(script)
	result := DriverBuildResult{WIMKey: key, WIMSHA256: wimHash, ScriptSHA256: hex.EncodeToString(scriptSum[:])}
	if err := m.publishDriverConfig(req, result, script); err != nil {
		return DriverBuildResult{}, err
	}
	return result, nil
}

func (m *DriverManager) CheckConfig(ctx context.Context, req DriverBuildRequest, result DriverBuildResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateBuildRequest(req); err != nil {
		return err
	}
	source := filepath.Join(m.ProfileRoot, strconv.FormatInt(req.ProfileID, 10), "boot.wim")
	sourceHash, _, err := hashFile(source)
	if err != nil {
		return err
	}
	if driverWIMKey(sourceHash, req.PackageINFs) != result.WIMKey {
		return fmt.Errorf("driver WIM key differs")
	}
	for _, selection := range req.PackageINFs {
		if err := m.checkPackage(selection.PackageSHA256); err != nil {
			return err
		}
	}
	actualWIM, _, err := hashFile(filepath.Join(m.WIMRoot, result.WIMKey, "boot.wim"))
	if err != nil || actualWIM != result.WIMSHA256 {
		return fmt.Errorf("derived WinPE WIM differs")
	}
	final := filepath.Join(m.ConfigRoot, strconv.FormatInt(req.ConfigID, 10))
	stored, err := os.ReadFile(filepath.Join(final, "build.json"))
	if err != nil {
		return err
	}
	var manifest driverConfigManifest
	if err := json.Unmarshal(stored, &manifest); err != nil {
		return err
	}
	if !sameDriverBuild(req, manifest.Request) || manifest.Result != result {
		return fmt.Errorf("driver configuration differs")
	}
	actual, err := os.ReadFile(filepath.Join(final, "omb-setup.cmd"))
	if err != nil {
		return fmt.Errorf("published WinPE script is unavailable")
	}
	// Published configurations freeze their script as well as their WIM. Check
	// those bytes against the manifest and caller snapshot, not a newer recipe.
	scriptSum := sha256.Sum256(actual)
	if hex.EncodeToString(scriptSum[:]) != result.ScriptSHA256 {
		return fmt.Errorf("published WinPE script differs")
	}
	return ctx.Err()
}

func (m *DriverManager) validateBuildRequest(req DriverBuildRequest) error {
	if req.ConfigID <= 0 || req.ProfileID <= 0 || req.AnswerID < 0 || len(req.PackageINFs) == 0 || len(req.PackageINFs) > 128 {
		return fmt.Errorf("invalid driver configuration")
	}
	for _, selected := range req.PackageINFs {
		if !validDriverHash(selected.PackageSHA256) || selected.INFPath == "" || path.IsAbs(selected.INFPath) || path.Clean(selected.INFPath) != selected.INFPath || strings.ContainsAny(selected.INFPath, "\\\x00") {
			return fmt.Errorf("invalid INF selection")
		}
		if err := m.checkPackage(selected.PackageSHA256); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(m.Root, selected.PackageSHA256, "inventory.json"))
		if err != nil {
			return err
		}
		var inventory drivers.Inventory
		if err := json.Unmarshal(data, &inventory); err != nil {
			return err
		}
		found := false
		for _, inf := range inventory.INFs {
			if inf.Path == selected.INFPath && inf.Architecture == "amd64" {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("selected INF is missing or not x64")
		}
	}
	return nil
}

func (m *DriverManager) driverScript(req DriverBuildRequest) ([]byte, error) {
	root := filepath.Join(m.ProfileRoot, strconv.FormatInt(req.ProfileID, 10))
	if err := requireRegularISOFile(root, "omb-setup.cmd"); err != nil {
		return nil, err
	}
	base, err := os.ReadFile(filepath.Join(root, "omb-setup.cmd"))
	if err != nil {
		return nil, err
	}
	if req.AnswerID > 0 {
		base, err = winpe.RenderAnswerStartup(base, req.ProfileID)
		if err != nil {
			return nil, err
		}
	}
	paths := make([]string, 0, len(req.PackageINFs))
	for _, selected := range req.PackageINFs {
		paths = append(paths, path.Join("omb-drivers", selected.PackageSHA256, selected.INFPath))
	}
	return winpe.RenderDriverStartup(base, paths)
}

func driverWIMKey(sourceHash string, selected []SelectedINF) string {
	h := sha256.New()
	_, _ = io.WriteString(h, sourceHash+"\x00")
	for _, item := range selected {
		_, _ = io.WriteString(h, item.PackageSHA256+"\x00"+item.INFPath+"\x00")
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (m *DriverManager) ensureDriverWIM(ctx context.Context, source string, sourceSize int64, key string, selected []SelectedINF) (string, error) {
	final := filepath.Join(m.WIMRoot, key)
	if _, err := os.Lstat(final); err == nil {
		return readCachedDriverWIM(final)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	free := m.FreeBytes
	if free == nil {
		free = driverFreeBytes
	}
	available, err := free(m.WIMRoot)
	if err != nil {
		return "", err
	}
	if available < uint64(sourceSize)*2+(512<<20) {
		return "", fmt.Errorf("insufficient space for derived WinPE WIM")
	}
	stage, err := os.MkdirTemp(m.WIMRoot, ".driver-wim-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	copyPath := filepath.Join(stage, "boot.wim")
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	out, err := os.OpenFile(copyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		in.Close()
		return "", err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := in.Close()
	writeCloseErr := out.Close()
	if copyErr != nil || closeErr != nil || writeCloseErr != nil {
		return "", fmt.Errorf("copy source WinPE WIM failed")
	}
	runner := m.Runner
	if runner == nil {
		runner = ExecDriverCommandRunner{}
	}
	info, err := runner.Run(ctx, "", "wimlib-imagex", "info", copyPath)
	if err != nil {
		return "", err
	}
	match := bootIndexLine.FindSubmatch(info)
	if len(match) != 2 || string(match[1]) == "0" {
		return "", fmt.Errorf("WinPE WIM has no boot index")
	}
	var commands strings.Builder
	seen := map[string]bool{}
	for _, item := range selected {
		if seen[item.PackageSHA256] {
			continue
		}
		seen[item.PackageSHA256] = true
		src := filepath.Join(m.Root, item.PackageSHA256, "files")
		if strings.ContainsAny(src, "\"\r\n") {
			return "", fmt.Errorf("driver storage path cannot be quoted")
		}
		fmt.Fprintf(&commands, "add \"%s\" \"/Windows/System32/omb-drivers/%s\"\n", src, item.PackageSHA256)
	}
	if _, err := runner.Run(ctx, commands.String(), "wimlib-imagex", "update", copyPath, string(match[1])); err != nil {
		return "", err
	}
	if _, err := runner.Run(ctx, "", "wimlib-imagex", "verify", copyPath); err != nil {
		return "", err
	}
	wimHash, _, err := hashFile(copyPath)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(stage, "sha256"), []byte(wimHash), 0644); err != nil {
		return "", err
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return "", err
	}
	if err := os.Rename(stage, final); err != nil {
		return "", err
	}
	return wimHash, nil
}

func readCachedDriverWIM(root string) (string, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("cached WinPE WIM is unavailable")
	}
	want, err := os.ReadFile(filepath.Join(root, "sha256"))
	if err != nil {
		return "", err
	}
	if !validDriverHash(string(want)) {
		return "", fmt.Errorf("cached WinPE WIM hash is invalid")
	}
	actual, _, err := hashFile(filepath.Join(root, "boot.wim"))
	if err != nil || actual != string(want) {
		return "", fmt.Errorf("cached WinPE WIM differs")
	}
	return actual, nil
}

func (m *DriverManager) publishDriverConfig(req DriverBuildRequest, result DriverBuildResult, script []byte) error {
	final := filepath.Join(m.ConfigRoot, strconv.FormatInt(req.ConfigID, 10))
	if _, err := os.Lstat(final); err == nil {
		stored, err := os.ReadFile(filepath.Join(final, "build.json"))
		if err != nil {
			return err
		}
		var manifest driverConfigManifest
		if err := json.Unmarshal(stored, &manifest); err != nil {
			return err
		}
		if !sameDriverBuild(req, manifest.Request) || manifest.Result != result {
			return fmt.Errorf("driver configuration is immutable")
		}
		actual, err := os.ReadFile(filepath.Join(final, "omb-setup.cmd"))
		if err != nil || !bytes.Equal(actual, script) {
			return fmt.Errorf("existing driver script differs")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(m.ConfigRoot, ".driver-config-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	manifest, err := json.Marshal(driverConfigManifest{Request: req, Result: result})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "build.json"), manifest, 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "omb-setup.cmd"), script, 0644); err != nil {
		return err
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return err
	}
	return os.Rename(stage, final)
}

func sameDriverBuild(a, b DriverBuildRequest) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

func hashFile(name string) (string, int64, error) {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", 0, fmt.Errorf("required file is unavailable: %s", name)
	}
	f, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

func driverFreeBytes(root string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(root, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
