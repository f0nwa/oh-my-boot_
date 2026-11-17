package images

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

const MaxISOBytes int64 = 32 << 30

var (
	ErrInvalidISO        = errors.New("invalid ISO")
	ErrInvalidFolder     = errors.New("invalid or unavailable folder")
	ErrInsufficientSpace = errors.New("insufficient space")
	ErrISOExists         = errors.New("ISO already exists")
	ErrIncompleteUpload  = errors.New("incomplete upload")
)

type UploadResult struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func ValidISOName(name string) bool {
	if name == "" || len(name) > 240 || !utf8.ValidString(name) || !strings.EqualFold(filepath.Ext(name), ".iso") ||
		strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || strings.ContainsAny(name, `/\:*?"<>|`) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func existingFolder(root, relative string) (string, error) {
	if err := validateRootPath(root); err != nil {
		return "", err
	}
	if relative == "" {
		return root, nil
	}
	if len(relative) > 512 || !utf8.ValidString(relative) || filepath.IsAbs(relative) || strings.ContainsAny(relative, `\:*?"<>|`) {
		return "", fmt.Errorf("invalid folder")
	}
	parts := strings.Split(relative, "/")
	if len(parts) > 16 {
		return "", fmt.Errorf("folder is too deep")
	}
	current := root
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, " ") || strings.HasSuffix(part, ".") {
			return "", fmt.Errorf("invalid folder")
		}
		for _, r := range part {
			if unicode.IsControl(r) {
				return "", fmt.Errorf("invalid folder")
			}
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("folder is not a real directory")
		}
	}
	return current, nil
}

func ListFolders(root string) ([]string, error) {
	if err := validateRootPath(root); err != nil {
		return nil, err
	}
	folders := []string{""}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root || !entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err := existingFolder(root, filepath.ToSlash(rel)); err != nil {
			return filepath.SkipDir
		}
		if len(folders) >= 1000 {
			return fmt.Errorf("too many folders")
		}
		folders = append(folders, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(folders)
	return folders, nil
}

func ReceiveISO(ctx context.Context, root, folder, name string, body io.Reader, size int64) (UploadResult, error) {
	if !ValidISOName(name) || size <= 0 || size > MaxISOBytes {
		return UploadResult{}, ErrInvalidISO
	}
	directory, err := existingFolder(root, folder)
	if err != nil {
		return UploadResult{}, fmt.Errorf("%w: %v", ErrInvalidFolder, err)
	}
	var space syscall.Statfs_t
	if err := syscall.Statfs(directory, &space); err != nil {
		return UploadResult{}, err
	}
	if uint64(size) > space.Bavail*uint64(space.Bsize) {
		return UploadResult{}, ErrInsufficientSpace
	}
	destination := filepath.Join(directory, name)
	if _, err := os.Lstat(destination); err == nil {
		return UploadResult{}, ErrISOExists
	} else if !os.IsNotExist(err) {
		return UploadResult{}, err
	}
	temp, err := os.CreateTemp(directory, ".omb-upload-*.part")
	if err != nil {
		return UploadResult{}, err
	}
	defer os.Remove(temp.Name())
	hash := sha256.New()
	written, err := io.CopyN(io.MultiWriter(temp, hash), &contextReader{ctx: ctx, reader: body}, size)
	if err != nil || written != size {
		temp.Close()
		return UploadResult{}, fmt.Errorf("%w: %v", ErrIncompleteUpload, err)
	}
	var trailing [1]byte
	if n, err := body.Read(trailing[:]); n != 0 || err != io.EOF {
		temp.Close()
		return UploadResult{}, ErrInvalidISO
	}
	if err := ctx.Err(); err != nil {
		temp.Close()
		return UploadResult{}, err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return UploadResult{}, err
	}
	if err := temp.Chmod(0644); err != nil {
		temp.Close()
		return UploadResult{}, err
	}
	if err := temp.Close(); err != nil {
		return UploadResult{}, err
	}
	if err := promoteNoReplace(temp.Name(), destination); err != nil {
		return UploadResult{}, err
	}
	path := name
	if folder != "" {
		path = folder + "/" + name
	}
	return UploadResult{Path: path, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(b)
}
