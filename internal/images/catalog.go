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
	Subpath    string     `json:"subpath,omitempty"`
	State      string     `json:"state"`
	Error      string     `json:"error,omitempty"`
	LastScanAt *time.Time `json:"last_scan_at,omitempty"`
}

type Image struct {
	StorageID int64     `json:"storage_id"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	Modified  time.Time `json:"modified"`
	Present   bool      `json:"present"`
}

// Scan inventories regular ISO files without following symlinks or changing files.
func Scan(root string) ([]Image, error) {
	if err := validateRootPath(root); err != nil {
		return nil, err
	}
	images := []Image{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(entry.Name()), ".iso") {
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if !fileInfo.Mode().IsRegular() {
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
		return nil, err
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Path < images[j].Path })
	return images, nil
}
