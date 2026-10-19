package drivers

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

const (
	MaxArchiveBytes   int64 = 256 << 20
	MaxExtractedBytes int64 = 512 << 20
	MaxFiles                = 4096
	MaxPathComponents       = 16
	maxINFBytes       int64 = 4 << 20
)

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type INF struct {
	Path         string   `json:"path"`
	Architecture string   `json:"architecture"`
	Class        string   `json:"class"`
	HardwareIDs  []string `json:"hardware_ids"`
}

type Inventory struct {
	SHA256 string `json:"sha256"`
	Files  []File `json:"files"`
	INFs   []INF  `json:"infs"`
}

type zipEntry struct {
	file *zip.File
	name string
}

var (
	driverFileRef = regexp.MustCompile(`(?i)\b[a-z0-9_.-]+\.(?:sys|cat)\b`)
	hardwareIDRef = regexp.MustCompile(`(?i)(?:PCI\\VEN_[A-Z0-9_&]+|USB\\VID_[A-Z0-9_&]+)`)
)

// InspectZIP checks an upload and returns a hash independent of ZIP metadata,
// member order, compression method, and a common enclosing directory.
func InspectZIP(src io.ReaderAt, size int64) (Inventory, error) {
	var result Inventory
	entries, err := zipEntries(src, size)
	if err != nil {
		return result, err
	}
	byBase := make(map[string]bool, len(entries))
	infData := make(map[string][]byte)
	var total int64
	for _, entry := range entries {
		if entry.file.UncompressedSize64 > uint64(MaxExtractedBytes-total) {
			return result, fmt.Errorf("driver package exceeds extracted size limit")
		}
		r, err := entry.file.Open()
		if err != nil {
			return result, err
		}
		h := sha256.New()
		var dst io.Writer = h
		var infContent strings.Builder
		if strings.EqualFold(path.Ext(entry.name), ".inf") {
			if int64(entry.file.UncompressedSize64) > maxINFBytes {
				r.Close()
				return result, fmt.Errorf("INF is too large: %s", entry.name)
			}
			dst = io.MultiWriter(h, &infContent)
		}
		written, copyErr := io.Copy(dst, io.LimitReader(r, MaxExtractedBytes-total+1))
		closeErr := r.Close()
		if copyErr != nil || closeErr != nil {
			return result, errors.Join(copyErr, closeErr)
		}
		if written != int64(entry.file.UncompressedSize64) || written > MaxExtractedBytes-total {
			return result, fmt.Errorf("driver package member size differs: %s", entry.name)
		}
		total += written
		result.Files = append(result.Files, File{Path: entry.name, SHA256: hex.EncodeToString(h.Sum(nil)), Size: written})
		byBase[strings.ToLower(path.Base(entry.name))] = true
		if strings.EqualFold(path.Ext(entry.name), ".inf") {
			infData[entry.name] = []byte(infContent.String())
		}
	}
	if len(infData) == 0 {
		return result, fmt.Errorf("driver package contains no INF")
	}
	for name, raw := range infData {
		inf, refs := inspectINF(name, raw)
		for _, ref := range refs {
			if !byBase[strings.ToLower(ref)] {
				return Inventory{}, fmt.Errorf("INF %s references missing file %s", name, ref)
			}
		}
		result.INFs = append(result.INFs, inf)
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].Path < result.Files[j].Path })
	sort.Slice(result.INFs, func(i, j int) bool { return result.INFs[i].Path < result.INFs[j].Path })
	result.SHA256 = CanonicalHash(result.Files)
	return result, nil
}

// CanonicalHash identifies file content and normalized relative paths, not
// the ZIP container used to transport them.
func CanonicalHash(files []File) string {
	ordered := append([]File(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	h := sha256.New()
	for _, file := range ordered {
		writeManifestPart(h, file.Path)
		writeManifestPart(h, file.SHA256)
		writeManifestPart(h, fmt.Sprint(file.Size))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeManifestPart(h hash.Hash, part string) {
	_, _ = io.WriteString(h, part)
	_, _ = h.Write([]byte{0})
}

// ExtractZIP writes a checked package into an existing empty directory.
// The destination is replaced only after every file has been written.
func ExtractZIP(src io.ReaderAt, size int64, dest string) (Inventory, error) {
	result, err := InspectZIP(src, size)
	if err != nil {
		return Inventory{}, err
	}
	info, err := os.Lstat(dest)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Inventory{}, fmt.Errorf("extraction target must be a directory")
	}
	contents, err := os.ReadDir(dest)
	if err != nil || len(contents) != 0 {
		return Inventory{}, fmt.Errorf("extraction target must be empty")
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".driver-extract-")
	if err != nil {
		return Inventory{}, err
	}
	defer os.RemoveAll(stage)
	entries, err := zipEntries(src, size)
	if err != nil {
		return Inventory{}, err
	}
	for _, entry := range entries {
		target := filepath.Join(stage, filepath.FromSlash(entry.name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return Inventory{}, err
		}
		in, err := entry.file.Open()
		if err != nil {
			return Inventory{}, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			in.Close()
			return Inventory{}, err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := errors.Join(in.Close(), out.Close())
		if copyErr != nil || closeErr != nil {
			return Inventory{}, errors.Join(copyErr, closeErr)
		}
	}
	if err := os.Remove(dest); err != nil {
		return Inventory{}, err
	}
	if err := os.Rename(stage, dest); err != nil {
		_ = os.Mkdir(dest, 0700)
		return Inventory{}, err
	}
	return result, nil
}

func zipEntries(src io.ReaderAt, size int64) ([]zipEntry, error) {
	if size <= 0 || size > MaxArchiveBytes {
		return nil, fmt.Errorf("driver archive size is invalid")
	}
	zr, err := zip.NewReader(src, size)
	if err != nil {
		return nil, err
	}
	entries := make([]zipEntry, 0, len(zr.File))
	var total uint64
	for _, file := range zr.File {
		name := file.Name
		if strings.HasSuffix(name, "/") {
			if err := validPackagePath(strings.TrimSuffix(name, "/")); err != nil {
				return nil, err
			}
			if file.Mode()&os.ModeSymlink != 0 || (file.Mode().IsRegular() && file.UncompressedSize64 != 0) {
				return nil, fmt.Errorf("invalid directory entry: %s", name)
			}
			continue
		}
		if err := validPackagePath(name); err != nil {
			return nil, err
		}
		if !file.Mode().IsRegular() {
			return nil, fmt.Errorf("driver package contains a non-regular file: %s", name)
		}
		if len(entries) >= MaxFiles || file.UncompressedSize64 > uint64(MaxExtractedBytes)-total {
			return nil, fmt.Errorf("driver package exceeds file or size limit")
		}
		total += file.UncompressedSize64
		entries = append(entries, zipEntry{file: file, name: name})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("driver archive is empty")
	}
	// An enclosing directory is packaging, not part of the driver's identity.
	for {
		var first string
		all := true
		for _, entry := range entries {
			parts := strings.SplitN(entry.name, "/", 2)
			if len(parts) < 2 {
				all = false
				break
			}
			if first == "" {
				first = parts[0]
			} else if !strings.EqualFold(first, parts[0]) {
				all = false
				break
			}
		}
		if !all {
			break
		}
		for i := range entries {
			entries[i].name = strings.SplitN(entries[i].name, "/", 2)[1]
		}
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		folded := strings.ToLower(entry.name)
		if seen[folded] {
			return nil, fmt.Errorf("driver package contains duplicate path: %s", entry.name)
		}
		seen[folded] = true
	}
	return entries, nil
}

func validPackagePath(name string) error {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\:\x00") {
		return fmt.Errorf("unsafe driver package path: %q", name)
	}
	parts := strings.Split(name, "/")
	if len(parts) > MaxPathComponents {
		return fmt.Errorf("driver package path is too deep")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return fmt.Errorf("unsafe driver package path: %q", name)
		}
	}
	return nil
}

// ValidateUploadPath applies the archive path rules to a browser folder item.
func ValidateUploadPath(name string) error { return validPackagePath(name) }

func inspectINF(name string, raw []byte) (INF, []string) {
	if len(raw) >= 2 && raw[0] == 0xff && raw[1] == 0xfe {
		units := make([]uint16, 0, (len(raw)-2)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
		}
		raw = []byte(string(utf16.Decode(units)))
	}
	text := string(raw)
	lower := strings.ToLower(text)
	architecture := "unknown"
	if strings.Contains(lower, "ntamd64") {
		architecture = "amd64"
	} else if strings.Contains(lower, "ntx86") {
		architecture = "x86"
	} else if strings.Contains(lower, "ntarm64") {
		architecture = "arm64"
	}
	inf := INF{Path: name, Architecture: architecture, HardwareIDs: []string{}}
	refs := map[string]bool{}
	ids := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, ";", 2)[0])
		if strings.HasPrefix(strings.ToLower(line), "class=") {
			inf.Class = strings.Trim(strings.TrimSpace(strings.SplitN(line, "=", 2)[1]), `"`)
		}
		for _, ref := range driverFileRef.FindAllString(line, -1) {
			refs[strings.ToLower(ref)] = true
		}
		for _, id := range hardwareIDRef.FindAllString(line, -1) {
			ids[strings.ToUpper(id)] = true
		}
	}
	references := make([]string, 0, len(refs))
	for ref := range refs {
		references = append(references, ref)
	}
	sort.Strings(references)
	for id := range ids {
		inf.HardwareIDs = append(inf.HardwareIDs, id)
	}
	sort.Strings(inf.HardwareIDs)
	return inf, references
}
