package boot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"omb/internal/autoinstall"
	"os"
	"path/filepath"
)

var errUbuntuSource = errors.New("Ubuntu source metadata unavailable; prepare the configured ISO again")

type UbuntuSource struct {
	isoPath, assetDir string
	catalog           autoinstall.SourceCatalog
	info              map[string]os.FileInfo
	err               error
}
type ubuntuSourceManifest struct {
	Version   int               `json:"version"`
	ISOSHA256 string            `json:"iso_sha256"`
	ISOSize   int64             `json:"iso_size"`
	Files     map[string]string `json:"files"`
}

func NewUbuntuSource(isoPath, assetDir string) *UbuntuSource {
	s := &UbuntuSource{isoPath: isoPath, assetDir: filepath.Join(assetDir, "ubuntu"), info: map[string]os.FileInfo{}}
	s.err = s.load()
	return s
}
func (s *UbuntuSource) read(path string, limit int64) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errUbuntuSource
	}
	if old := s.info[path]; old != nil && !sameUbuntuSourceInfo(old, info) {
		return nil, errUbuntuSource
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, errUbuntuSource
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return nil, errUbuntuSource
	}
	data, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(data)) > limit {
		return nil, errUbuntuSource
	}
	after, e := f.Stat()
	if e != nil || !sameUbuntuSourceInfo(opened, after) {
		return nil, errUbuntuSource
	}
	s.info[path] = after
	return data, nil
}
func (s *UbuntuSource) hash(path, want string, limit int64) error {
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return errUbuntuSource
	}
	f, e := os.Open(path)
	if e != nil {
		return errUbuntuSource
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return errUbuntuSource
	}
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil || hex.EncodeToString(h.Sum(nil)) != want {
		return errUbuntuSource
	}
	after, e := f.Stat()
	if e != nil || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return errUbuntuSource
	}
	s.info[path] = opened
	return nil
}
func (s *UbuntuSource) load() error {
	if !filepath.IsAbs(s.isoPath) {
		return errUbuntuSource
	}
	info, e := os.Lstat(s.assetDir)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errUbuntuSource
	}
	s.info[s.assetDir] = info
	raw, e := s.read(filepath.Join(s.assetDir, "manifest.json"), 64*1024)
	if e != nil {
		return e
	}
	var m ubuntuSourceManifest
	if json.Unmarshal(raw, &m) != nil || m.Version != 1 || m.ISOSize <= 0 || !validSHA256(m.ISOSHA256) || len(m.Files) != 4 {
		return errUbuntuSource
	}
	if e = s.hash(s.isoPath, m.ISOSHA256, m.ISOSize); e != nil {
		return e
	}
	if s.info[s.isoPath].Size() != m.ISOSize {
		return errUbuntuSource
	}
	for _, n := range []string{"vmlinuz", "initrd", "bootx64.efi"} {
		want := m.Files[n]
		if !validSHA256(want) {
			return errUbuntuSource
		}
		if e = s.hash(filepath.Join(s.assetDir, n), want, 1<<32); e != nil {
			return e
		}
	}
	data, e := s.read(filepath.Join(s.assetDir, "install-sources.yaml"), 64*1024)
	if e != nil {
		return e
	}
	want := m.Files["install-sources.yaml"]
	digest := sha256.Sum256(data)
	if !validSHA256(want) || hex.EncodeToString(digest[:]) != want {
		return errUbuntuSource
	}
	options, e := autoinstall.ParseInstallSources(data)
	if e != nil {
		return errUbuntuSource
	}
	s.catalog = autoinstall.SourceCatalog{ISOSHA256: m.ISOSHA256, Sources: options}
	_, e = s.Catalog()
	return e
}
func (s *UbuntuSource) Catalog() (autoinstall.SourceCatalog, error) {
	if s == nil || s.err != nil {
		return autoinstall.SourceCatalog{}, errUbuntuSource
	}
	for path, old := range s.info {
		now, e := os.Lstat(path)
		if e != nil || !os.SameFile(old, now) || now.Mode() != old.Mode() || now.Size() != old.Size() || !now.ModTime().Equal(old.ModTime()) {
			return autoinstall.SourceCatalog{}, errUbuntuSource
		}
	}
	result := s.catalog
	result.Sources = append([]autoinstall.SourceOption(nil), result.Sources...)
	return result, nil
}

func sameUbuntuSourceInfo(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
