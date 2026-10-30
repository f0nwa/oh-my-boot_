package boot

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const MaxAutoinstallUserDataSize = 64 << 10

// AssetInfo describes the complete immutable NoCloud pair, never its contents.
type AssetInfo struct {
	UserDataSHA256 string `json:"user_data_sha256"`
	MetaDataSHA256 string `json:"meta_data_sha256"`
}

// AutoinstallAssets persists versioned NoCloud pairs under the Boot asset root.
// It has no in-memory inventory: checks also work after a process restart.
type AutoinstallAssets struct{ root string }

func NewAutoinstallAssets(assetDir string) (*AutoinstallAssets, error) {
	root, err := filepath.EvalSymlinks(assetDir)
	if err != nil {
		return nil, fmt.Errorf("resolve autoinstall asset root: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	dir, err := openAutoinstallDirectory(root, nil, false)
	if err != nil {
		return nil, err
	}
	dir.Close()
	return &AutoinstallAssets{root: root}, nil
}

func canonicalPositiveID(raw string) bool {
	id, err := strconv.ParseInt(raw, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == raw
}

func validSHA256(raw string) bool {
	if len(raw) != 64 {
		return false
	}
	for _, ch := range raw {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func validUbuntuAutoinstallAsset(name string) bool {
	parts := strings.Split(name, "/")
	return len(parts) == 4 && parts[0] == "ubuntu-autoinstall" && canonicalPositiveID(parts[1]) && validSHA256(parts[2]) && (parts[3] == "user-data" || parts[3] == "meta-data")
}

func autoinstallParts(id int64, digest string) ([]string, error) {
	if id <= 0 || !validSHA256(digest) {
		return nil, fmt.Errorf("invalid autoinstall installation ID or SHA-256")
	}
	return []string{"ubuntu-autoinstall", strconv.FormatInt(id, 10), digest}, nil
}

func autoinstallMetaData(id int64, digest string) []byte {
	return []byte(fmt.Sprintf("instance-id: pxe-installation-%d-%s\n", id, digest))
}
func assetDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// Stage writes and syncs both files in an unreachable sibling directory before
// atomically renaming it to its final digest. Existing versions are only checked.
func (a *AutoinstallAssets) Stage(id int64, digest string, userData []byte) (AssetInfo, error) {
	parts, err := autoinstallParts(id, digest)
	if err != nil {
		return AssetInfo{}, err
	}
	if len(userData) == 0 || len(userData) > MaxAutoinstallUserDataSize {
		return AssetInfo{}, fmt.Errorf("autoinstall user-data size is invalid")
	}
	if assetDigest(userData) != digest {
		return AssetInfo{}, fmt.Errorf("autoinstall user-data SHA-256 mismatch")
	}
	parent, err := openAutoinstallDirectory(a.root, parts[:2], true)
	if err != nil {
		return AssetInfo{}, err
	}
	defer parent.Close()
	existing, err := openAutoinstallChildDirectory(parent, digest)
	if err == nil {
		defer existing.Close()
		return checkAutoinstallPair(existing, id, digest)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return AssetInfo{}, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return AssetInfo{}, err
	}
	tempName := ".stage-" + hex.EncodeToString(random[:])
	if err := unix.Mkdirat(int(parent.Fd()), tempName, 0700); err != nil {
		return AssetInfo{}, err
	}
	temp, err := openAutoinstallChildDirectory(parent, tempName)
	if err != nil {
		_ = unix.Unlinkat(int(parent.Fd()), tempName, unix.AT_REMOVEDIR)
		return AssetInfo{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = unix.Unlinkat(int(temp.Fd()), "user-data", 0)
			_ = unix.Unlinkat(int(temp.Fd()), "meta-data", 0)
		}
		_ = temp.Close()
		if !published {
			_ = unix.Unlinkat(int(parent.Fd()), tempName, unix.AT_REMOVEDIR)
		}
	}()
	for _, file := range []struct {
		name string
		data []byte
	}{{"user-data", userData}, {"meta-data", autoinstallMetaData(id, digest)}} {
		if err := writeAutoinstallFile(temp, file.name, file.data); err != nil {
			return AssetInfo{}, err
		}
	}
	if err := temp.Sync(); err != nil {
		return AssetInfo{}, err
	}
	if err := unix.Renameat(int(parent.Fd()), tempName, int(parent.Fd()), digest); err != nil {
		// A concurrent identical stage may have published first. Never replace it.
		if info, checkErr := a.Check(id, digest); checkErr == nil {
			return info, nil
		}
		return AssetInfo{}, fmt.Errorf("publish autoinstall pair: %w", err)
	}
	published = true
	if err := parent.Sync(); err != nil {
		return AssetInfo{}, err
	}
	return a.Check(id, digest)
}

func (a *AutoinstallAssets) Check(id int64, digest string) (AssetInfo, error) {
	parts, err := autoinstallParts(id, digest)
	if err != nil {
		return AssetInfo{}, err
	}
	dir, err := openAutoinstallDirectory(a.root, parts, false)
	if err != nil {
		return AssetInfo{}, err
	}
	defer dir.Close()
	return checkAutoinstallPair(dir, id, digest)
}

func checkAutoinstallPair(dir *os.File, id int64, digest string) (AssetInfo, error) {
	userData, err := readAutoinstallFile(dir, "user-data", MaxAutoinstallUserDataSize)
	if err != nil {
		return AssetInfo{}, err
	}
	if len(userData) == 0 || assetDigest(userData) != digest {
		return AssetInfo{}, fmt.Errorf("stored autoinstall user-data SHA-256 mismatch")
	}
	metaData, err := readAutoinstallFile(dir, "meta-data", 256)
	if err != nil {
		return AssetInfo{}, err
	}
	if !bytes.Equal(metaData, autoinstallMetaData(id, digest)) {
		return AssetInfo{}, fmt.Errorf("stored autoinstall meta-data mismatch")
	}
	return AssetInfo{UserDataSHA256: digest, MetaDataSHA256: assetDigest(metaData)}, nil
}

// Descriptor-relative O_NOFOLLOW opens reject symlinks even when another local
// operation swaps a path component between checks and opens.
func openAutoinstallDirectory(root string, parts []string, create bool) (*os.File, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), root)
	for _, part := range parts {
		if create {
			err := unix.Mkdirat(int(dir.Fd()), part, 0700)
			if err != nil && !errors.Is(err, unix.EEXIST) {
				dir.Close()
				return nil, err
			}
			if err == nil {
				if err := dir.Sync(); err != nil {
					dir.Close()
					return nil, err
				}
			}
		}
		next, err := openAutoinstallChildDirectory(dir, part)
		dir.Close()
		if err != nil {
			return nil, err
		}
		dir = next
	}
	return dir, nil
}

func openAutoinstallChildDirectory(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func openAutoinstallFile(dir *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("autoinstall file is not regular")
	}
	return file, nil
}

func readAutoinstallFile(dir *os.File, name string, limit int) ([]byte, error) {
	file, err := openAutoinstallFile(dir, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("stored autoinstall file too large")
	}
	return data, nil
}

func writeAutoinstallFile(dir *os.File, name string, data []byte) error {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func openPublicAutoinstallAsset(root, name string) (*os.File, error) {
	parts := strings.Split(name, "/")
	dir, err := openAutoinstallDirectory(root, parts[:3], false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	if _, err := checkAutoinstallPair(dir, id, parts[2]); err != nil {
		return nil, err
	}
	return openAutoinstallFile(dir, parts[3])
}
