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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"omb/internal/boot"
)

type ubuntuRegistryRecord struct {
	ID      int64  `json:"id"`
	SHA256  string `json:"sha256"`
	ISOPath string `json:"iso_path"`
}
type ubuntuLegacyRef struct {
	ID     int64  `json:"id"`
	SHA256 string `json:"sha256"`
}

func validUbuntuDigest(digest string) bool {
	raw, err := hex.DecodeString(digest)
	return err == nil && len(raw) == sha256.Size && hex.EncodeToString(raw) == digest
}

// PinLegacy binds the existing published URLs to a registered ISO, but only
// after its prepared artifacts match all three files currently used by Boot.
func (m *UbuntuISOManager) PinLegacy(ctx context.Context, id int64, digest string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id <= 0 || !validUbuntuDigest(digest) || !filepath.IsAbs(m.RegistryRoot) || !filepath.IsAbs(m.AssetRoot) {
		return fmt.Errorf("invalid legacy source")
	}
	rootInfo, err := os.Lstat(m.RegistryRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || rootInfo.Mode().Perm()&0022 != 0 || !agentOwnsFile(rootInfo) {
		return fmt.Errorf("invalid source registry")
	}
	filename := filepath.Join(m.RegistryRoot, strconv.FormatInt(id, 10)+".json")
	raw, err := readLegacyRegistryFile(filename)
	if err != nil {
		return err
	}
	var rec ubuntuRegistryRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&rec) != nil || decoder.Decode(new(any)) != io.EOF || rec.ID != id || rec.SHA256 != digest || !filepath.IsAbs(rec.ISOPath) {
		return fmt.Errorf("invalid registered source")
	}
	legacyISO, err := legacyISOFromConfig(m.LegacyConfigPath)
	if err != nil {
		return err
	}
	configuredInfo, err := os.Lstat(legacyISO)
	if err != nil || !configuredInfo.Mode().IsRegular() {
		return fmt.Errorf("configured legacy ISO unavailable")
	}
	configuredPath, err := filepath.EvalSymlinks(legacyISO)
	if err != nil || configuredPath != rec.ISOPath {
		return fmt.Errorf("registered source differs from configured legacy ISO")
	}
	sourceDir := filepath.Join(m.AssetRoot, "ubuntu-sources", strconv.FormatInt(id, 10), digest)
	for _, dir := range []string{m.AssetRoot, filepath.Join(m.AssetRoot, "ubuntu-sources"), filepath.Join(m.AssetRoot, "ubuntu-sources", strconv.FormatInt(id, 10)), sourceDir, filepath.Join(m.AssetRoot, "ubuntu")} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || !agentOwnsFile(info) {
			return fmt.Errorf("unsafe source directory")
		}
	}
	catalog, err := boot.NewUbuntuSourceAt(rec.ISOPath, sourceDir).Catalog()
	if err != nil || catalog.ISOSHA256 != digest || len(catalog.Sources) == 0 {
		return fmt.Errorf("registered source is not verified")
	}
	for _, name := range []string{"vmlinuz", "initrd", "bootx64.efi"} {
		old, err := hashLegacyFile(ctx, filepath.Join(m.AssetRoot, "ubuntu", name))
		if err != nil {
			return err
		}
		current, err := hashLegacyFile(ctx, filepath.Join(sourceDir, name))
		if err != nil || old != current {
			return fmt.Errorf("legacy boot assets differ from source")
		}
	}
	body, err := json.Marshal(ubuntuLegacyRef{ID: id, SHA256: digest})
	if err != nil {
		return err
	}
	if current, err := legacyISOFromConfig(m.LegacyConfigPath); err != nil || current != legacyISO {
		return fmt.Errorf("legacy configuration changed")
	}
	target := filepath.Join(m.RegistryRoot, "legacy.json")
	if existing, err := readLegacyRegistryFile(target); err == nil {
		if bytes.Equal(existing, body) {
			return nil
		}
		return fmt.Errorf("legacy source is already pinned")
	} else if !os.IsNotExist(err) {
		return err
	}
	temp, err := os.CreateTemp(m.RegistryRoot, ".legacy-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0644); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(tempName, target); err != nil {
		return fmt.Errorf("cannot pin legacy source: %w", err)
	}
	return nil
}

func legacyISOFromConfig(configPath string) (string, error) {
	if !filepath.IsAbs(configPath) {
		return "", fmt.Errorf("legacy configuration unavailable")
	}
	raw, err := readLegacyRegistryFile(configPath)
	if err != nil {
		return "", fmt.Errorf("legacy configuration unavailable")
	}
	var value string
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "OMB_BOOT_UBUNTU_ISO" {
			continue
		}
		if found {
			return "", fmt.Errorf("legacy ISO configuration is ambiguous")
		}
		found = true
		value = strings.TrimSpace(v)
		if strings.HasPrefix(value, `"`) {
			value, err = strconv.Unquote(value)
			if err != nil {
				return "", fmt.Errorf("invalid legacy ISO configuration")
			}
		} else if strings.HasPrefix(value, "'") {
			if len(value) < 2 || !strings.HasSuffix(value, "'") {
				return "", fmt.Errorf("invalid legacy ISO configuration")
			}
			value = value[1 : len(value)-1]
		}
	}
	if !found || !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("legacy ISO configuration unavailable")
	}
	return value, nil
}

func readLegacyRegistryFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 4096 || !agentOwnsFile(info) {
		return nil, fmt.Errorf("invalid registry file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("registry file changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return nil, fmt.Errorf("invalid registry file")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("registry file changed")
	}
	return data, nil
}

func agentOwnsFile(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func hashLegacyFile(ctx context.Context, path string) (string, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return "", fmt.Errorf("legacy asset unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", fmt.Errorf("legacy asset changed")
	}
	h := sha256.New()
	if _, err := io.Copy(h, ubuntuContextReader{ctx: ctx, reader: file}); err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return "", fmt.Errorf("legacy asset changed")
	}
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) || current.Size() != opened.Size() || !current.ModTime().Equal(opened.ModTime()) {
		return "", fmt.Errorf("legacy asset changed")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
