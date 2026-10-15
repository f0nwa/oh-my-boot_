package boot

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Event struct {
	Kind       string    `json:"kind"`
	Path       string    `json:"path"`
	RemoteAddr string    `json:"remote_addr"`
	At         time.Time `json:"at"`
}

// NewHandler serves a menu snapshot and files confined to assetDir.
func NewHandler(assetDir string, menu []byte, record func(Event)) (http.Handler, error) {
	p, err := NewPublisher(filepath.Join(assetDir, ".legacy-menu-snapshot"), menu)
	if err != nil {
		return nil, err
	}
	return NewDynamicHandler(assetDir, p, record)
}

// NewDynamicHandler reads the current published menu for each client request.
func NewDynamicHandler(assetDir string, publisher *Publisher, record func(Event)) (http.Handler, error) {
	if publisher == nil {
		return nil, fmt.Errorf("publisher is required")
	}
	root, err := filepath.EvalSymlinks(assetDir)
	if err != nil {
		return nil, fmt.Errorf("resolve asset directory: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("asset directory is unavailable: %s", assetDir)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/boot/menu.ipxe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		menu, _ := publisher.Current()
		_, _ = w.Write(menu)
		if record != nil {
			record(Event{Kind: "menu", Path: r.URL.Path, RemoteAddr: r.RemoteAddr, At: time.Now().UTC()})
		}
	})
	mux.HandleFunc("/assets/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/assets/")
		if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
			http.NotFound(w, r)
			return
		}
		candidate := filepath.Join(root, filepath.FromSlash(name))
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			http.NotFound(w, r)
			return
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, resolved)
		if record != nil {
			record(Event{Kind: "asset", Path: r.URL.Path, RemoteAddr: r.RemoteAddr, At: time.Now().UTC()})
		}
	})
	return mux, nil
}
