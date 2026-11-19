package boot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type ubuntuRegistryConfig struct {
	ID      int64  `json:"id"`
	SHA256  string `json:"sha256"`
	ISOPath string `json:"iso_path"`
}

// DefaultUbuntuRegistryRoot contains Agent-managed source descriptors.
const DefaultUbuntuRegistryRoot = "/var/lib/omb-ubuntu-registry"

type ubuntuRegistryEntry struct {
	config ubuntuRegistryConfig
	source *UbuntuSource
}

type ubuntuLegacyConfig struct {
	ID     int64  `json:"id"`
	SHA256 string `json:"sha256"`
}

// Registry records are root-managed; client requests never supply ISO paths.
type ubuntuRegistry struct {
	root, assets string
	mu           sync.Mutex
	entries      map[string]ubuntuRegistryEntry
	locks        map[string]*sync.Mutex
	loadSource   func(string, string) *UbuntuSource
	transfers    *Transfers
}

// UbuntuRegistry shares verified source snapshots between the public and local APIs.
type UbuntuRegistry = ubuntuRegistry

func NewUbuntuRegistry(root, assets string) *UbuntuRegistry {
	if root == "" {
		root = DefaultUbuntuRegistryRoot
	}
	return &ubuntuRegistry{root: root, assets: assets}
}

func (r *ubuntuRegistry) legacy() (ubuntuLegacyConfig, error) {
	var config ubuntuLegacyConfig
	if !filepath.IsAbs(r.root) {
		return config, errUbuntuSource
	}
	configPath := filepath.Join(r.root, "legacy.json")
	info, err := os.Lstat(configPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 4096 {
		return config, errUbuntuSource
	}
	f, err := os.Open(configPath)
	if err != nil {
		return config, errUbuntuSource
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		f.Close()
		return config, errUbuntuSource
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	after, statErr := f.Stat()
	f.Close()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err != nil || statErr != nil || !sameUbuntuSourceInfo(opened, after) || len(raw) > 4096 || decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || config.ID <= 0 || !validSHA256(config.SHA256) {
		return config, errUbuntuSource
	}
	if _, err := r.source(config.ID, config.SHA256); err != nil {
		return config, errUbuntuSource
	}
	return config, nil
}

func (r *ubuntuRegistry) serveLegacy(w http.ResponseWriter, req *http.Request, name string) {
	config, err := r.legacy()
	if err != nil {
		http.NotFound(w, req)
		return
	}
	clone := req.Clone(req.Context())
	if name == "" {
		clone.URL.Path = fmt.Sprintf("/images/ubuntu/%d/%s.iso", config.ID, config.SHA256)
		r.serve(w, clone, true)
		return
	}
	if name != "vmlinuz" && name != "initrd" && name != "bootx64.efi" {
		http.NotFound(w, req)
		return
	}
	clone.URL.Path = fmt.Sprintf("/assets/ubuntu/%d/%s/%s", config.ID, config.SHA256, name)
	r.serve(w, clone, false)
}

func (r *ubuntuRegistry) source(id int64, digest string) (*UbuntuSource, error) {
	if id <= 0 || !validSHA256(digest) || !filepath.IsAbs(r.root) {
		return nil, errUbuntuSource
	}
	configPath := filepath.Join(r.root, strconv.FormatInt(id, 10)+".json")
	info, err := os.Lstat(configPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 4096 {
		return nil, errUbuntuSource
	}
	f, err := os.Open(configPath)
	if err != nil {
		return nil, errUbuntuSource
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		f.Close()
		return nil, errUbuntuSource
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	closedInfo, statErr := f.Stat()
	f.Close()
	if err != nil || statErr != nil || !sameUbuntuSourceInfo(opened, closedInfo) || len(raw) > 4096 {
		return nil, errUbuntuSource
	}
	var config ubuntuRegistryConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || config.ID != id || config.SHA256 != digest || !filepath.IsAbs(config.ISOPath) {
		return nil, errUbuntuSource
	}
	key := fmt.Sprintf("%d/%s", id, digest)
	r.mu.Lock()
	if r.locks == nil {
		r.locks = make(map[string]*sync.Mutex)
	}
	lock := r.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		r.locks[key] = lock
	}
	r.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	r.mu.Lock()
	cached, ok := r.entries[key]
	r.mu.Unlock()
	if ok && cached.config == config {
		if _, err := cached.source.Catalog(); err == nil {
			return cached.source, nil
		}
		r.mu.Lock()
		delete(r.entries, key)
		r.mu.Unlock()
	}
	root := filepath.Join(r.assets, "ubuntu-sources")
	for _, dir := range []string{root, filepath.Join(root, strconv.FormatInt(id, 10)), filepath.Join(root, strconv.FormatInt(id, 10), digest)} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errUbuntuSource
		}
	}
	loader := r.loadSource
	if loader == nil {
		loader = NewUbuntuSourceAt
	}
	source := loader(config.ISOPath, filepath.Join(root, strconv.FormatInt(id, 10), digest))
	catalog, err := source.Catalog()
	if err != nil || catalog.ISOSHA256 != digest {
		return nil, errUbuntuSource
	}
	r.mu.Lock()
	if r.entries == nil {
		r.entries = make(map[string]ubuntuRegistryEntry)
	}
	r.entries[key] = ubuntuRegistryEntry{config: config, source: source}
	r.mu.Unlock()
	return source, nil
}

func (r *ubuntuRegistry) serve(w http.ResponseWriter, req *http.Request, iso bool) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	client, err := netip.ParseAddrPort(req.RemoteAddr)
	if err != nil || !netip.MustParsePrefix("10.30.0.0/24").Contains(client.Addr()) {
		http.NotFound(w, req)
		return
	}
	prefix := "/assets/ubuntu/"
	if iso {
		prefix = "/images/ubuntu/"
	}
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, prefix), "/")
	if (iso && len(parts) != 2) || (!iso && len(parts) != 3) {
		http.NotFound(w, req)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != parts[0] {
		http.NotFound(w, req)
		return
	}
	digest := parts[1]
	if iso {
		if !strings.HasSuffix(digest, ".iso") {
			http.NotFound(w, req)
			return
		}
		digest = strings.TrimSuffix(digest, ".iso")
	} else if parts[2] != "vmlinuz" && parts[2] != "initrd" && parts[2] != "bootx64.efi" {
		http.NotFound(w, req)
		return
	}
	source, err := r.source(id, digest)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	filePath := source.isoPath
	if !iso {
		filePath = filepath.Join(source.assetDir, parts[2])
	}
	f, err := os.Open(filePath)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	expected := source.info[filePath]
	if err != nil || expected == nil || !sameUbuntuSourceInfo(expected, info) {
		http.NotFound(w, req)
		return
	}
	if iso {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	if r.transfers != nil {
		id, live := r.transfers.startWithCancel(req.URL.Path, req.RemoteAddr, info.Size(), cancelWriterDeadline(w))
		http.ServeContent(w, req, info.Name(), info.ModTime(), countingFile{File: f, sent: &live.sent, lastActivity: &live.lastActivity, canceled: &live.canceled})
		r.transfers.finish(id)
		return
	}
	http.ServeContent(w, req, info.Name(), info.ModTime(), f)
}
