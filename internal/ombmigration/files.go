package ombmigration

import (
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

type FileRecord struct {
	Path      string `json:"path"`
	Hash      string `json:"sha256,omitempty"`
	Mode      uint32 `json:"mode"`
	UID, GID  uint32
	Size      int64
	Modified  int64
	Directory bool
}

func safePath(root, p string) (string, error) {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return "", fmt.Errorf("noncanonical migration path")
	}
	if root == "" {
		root = "/"
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", fmt.Errorf("noncanonical fixture root")
	}
	full := filepath.Join(root, strings.TrimPrefix(p, "/"))
	cur := root
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		i, e := os.Lstat(cur)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return "", e
		}
		if i.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink in migration path: %s", p)
		}
	}
	return full, nil
}
func recordFile(root, p string) (FileRecord, error) {
	full, e := safePath(root, p)
	if e != nil {
		return FileRecord{}, e
	}
	i, e := os.Lstat(full)
	if e != nil {
		return FileRecord{}, e
	}
	r := FileRecord{Path: p, Mode: uint32(preservedMode(i.Mode())), Size: i.Size(), Modified: i.ModTime().UnixNano(), Directory: i.IsDir()}
	if s, ok := i.Sys().(*syscall.Stat_t); ok {
		r.UID = s.Uid
		r.GID = s.Gid
	}
	if i.IsDir() {
		return r, nil
	}
	if !i.Mode().IsRegular() {
		return r, fmt.Errorf("unsupported asset file: %s", p)
	}
	f, e := os.Open(full)
	if e != nil {
		return r, e
	}
	defer f.Close()
	r.Hash, e = hashReader(f)
	return r, e
}
func hashReader(r io.Reader) (string, error) {
	h := newHash()
	if _, e := io.Copy(h, r); e != nil {
		return "", e
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
func treeRecords(root, p string) ([]FileRecord, error) {
	full, e := safePath(root, p)
	if e != nil {
		return nil, e
	}
	if _, e = os.Lstat(full); os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	out := []FileRecord{}
	e = filepath.WalkDir(full, func(file string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, e := filepath.Rel(full, file)
		if e != nil {
			return e
		}
		logical := p
		if rel != "." {
			logical = filepath.Join(p, rel)
		}
		r, e := recordFile(root, logical)
		if e != nil {
			return e
		}
		out = append(out, r)
		return nil
	})
	return out, e
}
func evidenceHash(records []FileRecord) string {
	copy := append([]FileRecord{}, records...)
	sort.Slice(copy, func(i, j int) bool { return copy[i].Path < copy[j].Path })
	for i := range copy {
		if copy[i].Directory {
			copy[i].Modified = 0
			copy[i].Size = 0
		}
	}
	return hashBytes(mustJSON(copy))
}
func checkRecords(root string, expected []FileRecord) error {
	for _, want := range expected {
		got, e := recordFile(root, want.Path)
		if e != nil {
			return fmt.Errorf("preserved file unavailable: %s", want.Path)
		}
		if evidenceHash([]FileRecord{got}) != evidenceHash([]FileRecord{want}) {
			return fmt.Errorf("file differs: %s", want.Path)
		}
	}
	return nil
}
func copyTree(source, dest string) error {
	info, e := os.Lstat(source)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe source archive directory")
	}
	if e = os.MkdirAll(dest, 0700); e != nil {
		return e
	}
	var dirs []struct {
		p string
		i os.FileInfo
	}
	e = filepath.WalkDir(source, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		i, e := d.Info()
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(source, p)
		if e != nil {
			return e
		}
		to := filepath.Join(dest, rel)
		if d.IsDir() {
			if e = os.MkdirAll(to, 0700); e != nil {
				return e
			}
			dirs = append(dirs, struct {
				p string
				i os.FileInfo
			}{to, i})
			return nil
		}
		if !i.Mode().IsRegular() {
			return fmt.Errorf("unsupported source archive entry")
		}
		in, e := os.Open(p)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = io.Copy(out, in)
		if e == nil {
			e = out.Sync()
		}
		ce := out.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			return e
		}
		return preserveMetadata(to, i)
	})
	if e != nil {
		return e
	}
	for n := len(dirs) - 1; n >= 0; n-- {
		if e = preserveMetadata(dirs[n].p, dirs[n].i); e != nil {
			return e
		}
	}
	return nil
}
func preservedMode(mode os.FileMode) os.FileMode {
	return mode & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
}
func preserveMetadata(p string, i os.FileInfo) error {
	if s, ok := i.Sys().(*syscall.Stat_t); ok {
		if e := os.Chown(p, int(s.Uid), int(s.Gid)); e != nil {
			return e
		}
	}
	if e := os.Chmod(p, preservedMode(i.Mode())); e != nil {
		return e
	}
	return os.Chtimes(p, i.ModTime(), i.ModTime())
}
func writePreserving(p string, b []byte) error {
	i, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() {
		return fmt.Errorf("unsafe replacement file")
	}
	if e = atomicWrite(p, b, preservedMode(i.Mode())); e != nil {
		return e
	}
	if s, ok := i.Sys().(*syscall.Stat_t); ok {
		if e = os.Chown(p, int(s.Uid), int(s.Gid)); e != nil {
			return e
		}
	}
	if e = os.Chmod(p, preservedMode(i.Mode())); e != nil {
		return e
	}
	return os.Chtimes(p, time.Now(), i.ModTime())
}

func newHash() hash.Hash { return sha256.New() }
