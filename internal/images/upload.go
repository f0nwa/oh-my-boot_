package images

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
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

const (
	MaxISOBytes           int64 = 32 << 30
	streamSpaceCheckChunk int64 = 32 << 20
	streamSpaceHeadroom   uint64 = 16 << 20
)

var (
	ErrInvalidISO        = errors.New("invalid ISO")
	ErrInvalidFolder     = errors.New("invalid or unavailable folder")
	ErrInsufficientSpace = errors.New("insufficient space")
	ErrISOExists         = errors.New("ISO already exists")
	ErrIncompleteUpload  = errors.New("incomplete upload")
	ErrChecksumMismatch  = errors.New("checksum mismatch")
)

type ReceiveOptions struct {
	ChecksumAlgo  string
	ChecksumValue string
}

// ValidateChecksum validates and normalizes an optional checksum specification.
// Empty algo and value are valid (verification disabled).
func ValidateChecksum(algo, value string) (string, string, error) {
	a := strings.ToLower(strings.TrimSpace(algo))
	v := strings.ToLower(strings.TrimSpace(value))
	if a == "" {
		if v != "" {
			return "", "", fmt.Errorf("checksum algorithm is required")
		}
		return "", "", nil
	}
	var wantLen int
	switch a {
	case "sha256":
		wantLen = 64
	case "sha512":
		wantLen = 128
	case "md5":
		wantLen = 32
	default:
		return "", "", fmt.Errorf("unsupported checksum algorithm")
	}
	if len(v) != wantLen {
		return "", "", fmt.Errorf("invalid checksum length")
	}
	if _, err := hex.DecodeString(v); err != nil {
		return "", "", fmt.Errorf("invalid checksum hex")
	}
	return a, v, nil
}

type UploadResult struct {
	Path   string `json:"path"`
	Kind   string `json:"kind,omitempty"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ValidExpectedKind reports whether expectedKind is a valid expected_kind
// parameter for POST /upload.
func ValidExpectedKind(expectedKind string) bool {
	switch expectedKind {
	case "", ImageKindISO, ImageKindWIM, ImageKindESD, "wim_or_esd":
		return true
	default:
		return false
	}
}

// ValidImageName reports whether name is a safe file name matching expectedKind.
// Supported expectedKind values: "" or "iso", "wim", "esd", "wim_or_esd", "any".
func ValidImageName(name, expectedKind string) bool {
	if name == "" || len(name) > 240 || !utf8.ValidString(name) ||
		strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || strings.ContainsAny(name, `/\:*?"<>|`) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	kind, ok := ImageKindFromPath(name)
	if !ok {
		return false
	}
	switch expectedKind {
	case "", ImageKindISO:
		return kind == ImageKindISO
	case ImageKindWIM:
		return kind == ImageKindWIM
	case ImageKindESD:
		return kind == ImageKindESD
	case "wim_or_esd":
		return kind == ImageKindWIM || kind == ImageKindESD
	case "any":
		return true
	default:
		return false
	}
}

func ValidISOName(name string) bool {
	return ValidImageName(name, ImageKindISO)
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
	return ReceiveImage(ctx, root, folder, name, ImageKindISO, body, size)
}

func ReceiveImage(ctx context.Context, root, folder, name, expectedKind string, body io.Reader, size int64) (UploadResult, error) {
	return ReceiveImageWithOptions(ctx, root, folder, name, expectedKind, body, size, ReceiveOptions{})
}

func ReceiveImageWithOptions(ctx context.Context, root, folder, name, expectedKind string, body io.Reader, size int64, opts ReceiveOptions) (UploadResult, error) {
	streaming := size < 0
	if !ValidExpectedKind(expectedKind) || !ValidImageName(name, expectedKind) || (!streaming && (size <= 0 || size > MaxISOBytes)) {
		return UploadResult{}, ErrInvalidISO
	}
	algo, wantChecksum, err := ValidateChecksum(opts.ChecksumAlgo, opts.ChecksumValue)
	if err != nil {
		return UploadResult{}, fmt.Errorf("%w: %v", ErrInvalidISO, err)
	}
	kind, _ := ImageKindFromPath(name)
	directory, err := existingFolder(root, folder)
	if err != nil {
		return UploadResult{}, fmt.Errorf("%w: %v", ErrInvalidFolder, err)
	}
	var space syscall.Statfs_t
	if err := syscall.Statfs(directory, &space); err != nil {
		return UploadResult{}, err
	}
	availBytes := space.Bavail * uint64(space.Bsize)
	if !streaming {
		if uint64(size) > availBytes {
			return UploadResult{}, ErrInsufficientSpace
		}
	} else if availBytes <= streamSpaceHeadroom {
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
	sha256Hash := sha256.New()
	writers := []io.Writer{temp, sha256Hash}
	var extraHash hash.Hash
	switch algo {
	case "sha512":
		extraHash = sha512.New()
		writers = append(writers, extraHash)
	case "md5":
		extraHash = md5.New()
		writers = append(writers, extraHash)
	}
	dst := io.MultiWriter(writers...)
	src := &contextReader{ctx: ctx, reader: body}
	var written int64
	if !streaming {
		written, err = io.CopyN(dst, src, size)
		if err != nil || written != size {
			temp.Close()
			return UploadResult{}, fmt.Errorf("%w: %v", ErrIncompleteUpload, err)
		}
		var trailing [1]byte
		if n, err := body.Read(trailing[:]); n != 0 || err != io.EOF {
			temp.Close()
			return UploadResult{}, ErrInvalidISO
		}
	} else {
		buf := make([]byte, 256<<10)
		var sinceSpaceCheck int64
		for {
			n, readErr := src.Read(buf)
			if n > 0 {
				if written+int64(n) > MaxISOBytes {
					temp.Close()
					return UploadResult{}, ErrInvalidISO
				}
				if _, werr := dst.Write(buf[:n]); werr != nil {
					temp.Close()
					return UploadResult{}, fmt.Errorf("%w: %v", ErrIncompleteUpload, werr)
				}
				written += int64(n)
				sinceSpaceCheck += int64(n)
				if sinceSpaceCheck >= streamSpaceCheckChunk {
					sinceSpaceCheck = 0
					if err := syscall.Statfs(directory, &space); err == nil {
						if space.Bavail*uint64(space.Bsize) <= streamSpaceHeadroom {
							temp.Close()
							return UploadResult{}, ErrInsufficientSpace
						}
					}
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				temp.Close()
				return UploadResult{}, fmt.Errorf("%w: %v", ErrIncompleteUpload, readErr)
			}
		}
		if written <= 0 {
			temp.Close()
			return UploadResult{}, ErrIncompleteUpload
		}
	}
	if err := ctx.Err(); err != nil {
		temp.Close()
		return UploadResult{}, err
	}
	sha256Hex := hex.EncodeToString(sha256Hash.Sum(nil))
	if algo != "" {
		got := sha256Hex
		if extraHash != nil {
			got = hex.EncodeToString(extraHash.Sum(nil))
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(wantChecksum)) != 1 {
			temp.Close()
			return UploadResult{}, ErrChecksumMismatch
		}
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
	return UploadResult{Path: path, Kind: kind, Size: written, SHA256: sha256Hex}, nil
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
