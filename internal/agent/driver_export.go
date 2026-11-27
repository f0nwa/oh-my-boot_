package agent

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"omb/internal/drivers"
)

const maxDriverExportItems = 1024

var (
	ErrDriverPackageMissing = errors.New("driver package not found")
	ErrDriverPackageBusy    = errors.New("driver package is being exported")
	ErrInvalidDriverExport  = errors.New("invalid driver export request")
)

type DriverExportItem struct {
	SHA256 string `json:"sha256"`
	Folder string `json:"folder"`
}

type preparedDriverPackage struct {
	sha256   string
	folder   string
	fileRoot string
	files    []drivers.File
}

type PreparedDriverExport struct {
	manager   *DriverManager
	packages  []preparedDriverPackage
	held      []string
	closeOnce sync.Once
}

func (e *PreparedDriverExport) Close() {
	if e == nil {
		return
	}
	e.closeOnce.Do(func() {
		e.manager.releaseExport(e.held)
	})
}

func (m *DriverManager) releaseExport(hashes []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range hashes {
		if n := m.exporting[h] - 1; n > 0 {
			m.exporting[h] = n
		} else {
			delete(m.exporting, h)
		}
	}
}

func validExportFolder(name string) bool {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 {
		return false
	}
	if strings.Contains(name, "/") || strings.ContainsAny(name, "\\:*?\"<>|") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return drivers.ValidateUploadPath(name) == nil
}

func (m *DriverManager) PrepareExport(ctx context.Context, items []DriverExportItem) (*PreparedDriverExport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 || len(items) > maxDriverExportItems {
		return nil, ErrInvalidDriverExport
	}
	seenFolders := make(map[string]bool, len(items))
	for _, item := range items {
		if !validDriverHash(item.SHA256) || !validExportFolder(item.Folder) {
			return nil, ErrInvalidDriverExport
		}
		folded := strings.ToLower(item.Folder)
		if seenFolders[folded] {
			return nil, ErrInvalidDriverExport
		}
		seenFolders[folded] = true
	}

	if !filepath.IsAbs(m.Root) {
		return nil, fmt.Errorf("driver root must be absolute")
	}
	rootInfo, err := os.Lstat(m.Root)
	if os.IsNotExist(err) {
		return nil, ErrDriverPackageMissing
	}
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("driver root is unavailable")
	}

	verifiedByHash := make(map[string]preparedDriverPackage, len(items))
	prepared := make([]preparedDriverPackage, 0, len(items))
	var uniqueHashes []string

	for _, item := range items {
		cached, ok := verifiedByHash[item.SHA256]
		if !ok {
			pkg, err := m.inspectPackageForExport(item.SHA256)
			if err != nil {
				return nil, err
			}
			verifiedByHash[item.SHA256] = pkg
			uniqueHashes = append(uniqueHashes, item.SHA256)
			cached = pkg
		}
		cached.folder = item.Folder
		prepared = append(prepared, cached)
	}

	if m.exporting == nil {
		m.exporting = make(map[string]int, len(uniqueHashes))
	}
	for _, h := range uniqueHashes {
		m.exporting[h]++
	}
	return &PreparedDriverExport{
		manager:  m,
		packages: prepared,
		held:     uniqueHashes,
	}, nil
}

func (m *DriverManager) inspectPackageForExport(hash string) (preparedDriverPackage, error) {
	pkgDir := filepath.Join(m.Root, hash)
	info, err := os.Lstat(pkgDir)
	if os.IsNotExist(err) {
		return preparedDriverPackage{}, ErrDriverPackageMissing
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return preparedDriverPackage{}, fmt.Errorf("driver package is unavailable")
	}
	manifestPath := filepath.Join(pkgDir, "inventory.json")
	manifestInfo, err := os.Lstat(manifestPath)
	if os.IsNotExist(err) {
		return preparedDriverPackage{}, ErrDriverPackageMissing
	}
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Size() > 1<<20 {
		return preparedDriverPackage{}, fmt.Errorf("driver package inventory is unavailable")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return preparedDriverPackage{}, err
	}
	var inventory drivers.Inventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		return preparedDriverPackage{}, err
	}
	if inventory.SHA256 != hash || drivers.CanonicalHash(inventory.Files) != hash || len(inventory.INFs) == 0 || len(inventory.Files) == 0 {
		return preparedDriverPackage{}, fmt.Errorf("driver package inventory differs")
	}

	fileRoot := filepath.Join(pkgDir, "files")
	frInfo, err := os.Lstat(fileRoot)
	if os.IsNotExist(err) {
		return preparedDriverPackage{}, ErrDriverPackageMissing
	}
	if err != nil || !frInfo.IsDir() || frInfo.Mode()&os.ModeSymlink != 0 {
		return preparedDriverPackage{}, fmt.Errorf("driver package files directory is unavailable")
	}

	ordered := append([]drivers.File(nil), inventory.Files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })

	checkedDirs := map[string]bool{fileRoot: true}
	for _, f := range ordered {
		if drivers.ValidateUploadPath(f.Path) != nil {
			return preparedDriverPackage{}, fmt.Errorf("invalid driver package inventory path")
		}
		full := filepath.Join(fileRoot, filepath.FromSlash(f.Path))
		// Ensure no intermediate directory is a symlink.
		for dir := filepath.Dir(full); !checkedDirs[dir] && strings.HasPrefix(dir, fileRoot+string(filepath.Separator)); dir = filepath.Dir(dir) {
			dInfo, err := os.Lstat(dir)
			if err != nil || !dInfo.IsDir() || dInfo.Mode()&os.ModeSymlink != 0 {
				return preparedDriverPackage{}, fmt.Errorf("driver package subdirectory is unsafe")
			}
			checkedDirs[dir] = true
		}
		st, err := os.Lstat(full)
		if os.IsNotExist(err) {
			return preparedDriverPackage{}, ErrDriverPackageMissing
		}
		if err != nil || !st.Mode().IsRegular() || st.Size() != f.Size {
			return preparedDriverPackage{}, fmt.Errorf("driver package file differs: %s", f.Path)
		}
	}

	return preparedDriverPackage{
		sha256:   hash,
		fileRoot: fileRoot,
		files:    ordered,
	}, nil
}

func (e *PreparedDriverExport) Stream(ctx context.Context, dst io.Writer) error {
	zw := zip.NewWriter(dst)
	for _, pkg := range e.packages {
		for _, file := range pkg.files {
			if err := ctx.Err(); err != nil {
				_ = zw.Close()
				return err
			}
			full := filepath.Join(pkg.fileRoot, filepath.FromSlash(file.Path))
			st, err := os.Lstat(full)
			if err != nil || !st.Mode().IsRegular() || st.Size() != file.Size {
				_ = zw.Close()
				return fmt.Errorf("driver package file unavailable: %s", file.Path)
			}
			f, err := os.Open(full)
			if err != nil {
				_ = zw.Close()
				return err
			}
			hdr := &zip.FileHeader{
				Name:     pkg.folder + "/" + file.Path,
				Method:   zip.Deflate,
				Modified: st.ModTime().UTC(),
			}
			out, err := zw.CreateHeader(hdr)
			if err != nil {
				f.Close()
				_ = zw.Close()
				return err
			}
			h := sha256.New()
			written, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(f, file.Size+1))
			closeErr := f.Close()
			if copyErr != nil || closeErr != nil {
				_ = zw.Close()
				return errors.Join(copyErr, closeErr)
			}
			if written != file.Size || hex.EncodeToString(h.Sum(nil)) != file.SHA256 {
				_ = zw.Close()
				return fmt.Errorf("driver package checksum mismatch: %s", file.Path)
			}
		}
	}
	return zw.Close()
}
