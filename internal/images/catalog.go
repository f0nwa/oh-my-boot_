package images

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Storage struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	Root       string     `json:"root"`
	Key        string     `json:"key,omitempty"`
	Server     string     `json:"server,omitempty"`
	Share      string     `json:"share,omitempty"`
	Subpath        string     `json:"subpath,omitempty"`
	ExportPath     string     `json:"export_path,omitempty"`
	NFSVersion     string     `json:"nfs_version,omitempty"`
	UploadsEnabled bool       `json:"uploads_enabled,omitempty"`
	State          string     `json:"state"`
	Error      string     `json:"error,omitempty"`
	LastScanAt *time.Time `json:"last_scan_at,omitempty"`
	// Filled when the catalog is served, not stored: the size of the file
	// system under Root and what is free on it, and whether ISO files can be
	// uploaded into it.
	Total   int64 `json:"total,omitempty"`
	Free    int64 `json:"free,omitempty"`
	Uploads bool  `json:"uploads,omitempty"`
	// ISOBytes sums present ISO files in the catalog; OtherBytes is the size of
	// other regular files recorded during the latest scan.
	ISOBytes   int64 `json:"iso_bytes,omitempty"`
	OtherBytes int64 `json:"other_bytes,omitempty"`
}

type Image struct {
	FirstSeenAt *time.Time `json:"first_seen_at,omitempty"`
	StorageID   int64      `json:"storage_id"`
	Path        string     `json:"path"`
	Size        int64      `json:"size"`
	Modified    time.Time  `json:"modified"`
	Present     bool       `json:"present"`
}

// ScanResult holds the ISO inventory and total size of non-ISO regular files
// discovered in a storage root.
type ScanResult struct {
	Images     []Image
	OtherBytes int64
}

// Scan inventories regular ISO files and totals other regular files without
// following symlinks or changing files.
func Scan(root string) (ScanResult, error) {
	if err := validateRootPath(root); err != nil {
		return ScanResult{}, err
	}
	images := []Image{}
	var otherBytes int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if !fileInfo.Mode().IsRegular() {
			return nil
		}
		name := entry.Name()
		if !strings.EqualFold(filepath.Ext(name), ".iso") {
			if strings.HasPrefix(name, ".omb-upload-") && strings.HasSuffix(name, ".part") {
				return nil
			}
			otherBytes += fileInfo.Size()
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		images = append(images, Image{Path: filepath.ToSlash(rel), Size: fileInfo.Size(), Modified: fileInfo.ModTime(), Present: true})
		return nil
	})
	if err != nil {
		return ScanResult{}, err
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Path < images[j].Path })
	return ScanResult{Images: images, OtherBytes: otherBytes}, nil
}
