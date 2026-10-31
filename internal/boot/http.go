package boot

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Event struct {
	Kind       string    `json:"kind"`
	Path       string    `json:"path"`
	RemoteAddr string    `json:"remote_addr"`
	MAC        string    `json:"mac,omitempty"`
	At         time.Time `json:"at"`
	// Status, Bytes and Size describe how a file request ended: the HTTP
	// status, the body bytes sent and the file's size. Zero in events
	// logged before 23.10.2026 and for the menu.
	Status int   `json:"status,omitempty"`
	Bytes  int64 `json:"bytes,omitempty"`
	Size   int64 `json:"size,omitempty"`
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
	return NewDynamicHandlerWithISO(assetDir, publisher, record, "")
}

// NewDynamicHandlerWithISO exposes one explicitly configured Ubuntu ISO.
// The rest of its storage is never mapped into the HTTP server.
func NewDynamicHandlerWithISO(assetDir string, publisher *Publisher, record func(Event), isoPath string) (http.Handler, error) {
	return NewDynamicHandlerWithTransfers(assetDir, publisher, record, isoPath, nil)
}

// NewDynamicHandlerWithTransfers also lists the files being sent in transfers.
func NewDynamicHandlerWithTransfers(assetDir string, publisher *Publisher, record func(Event), isoPath string, transfers *Transfers) (http.Handler, error) {
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
	if isoPath != "" {
		if !filepath.IsAbs(isoPath) {
			return nil, fmt.Errorf("Ubuntu ISO path must be absolute")
		}
		mux.HandleFunc("/images/ubuntu-desktop.iso", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			info, err := os.Lstat(isoPath)
			if err != nil || !info.Mode().IsRegular() {
				http.NotFound(w, r)
				return
			}
			file, err := os.Open(isoPath)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer file.Close()
			openedInfo, err := file.Stat()
			if err != nil || !openedInfo.Mode().IsRegular() {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			http.ServeContent(w, r, "ubuntu-desktop.iso", openedInfo.ModTime(), file)
			if record != nil {
				record(Event{Kind: "iso", Path: r.URL.Path, RemoteAddr: r.RemoteAddr, At: time.Now().UTC()})
			}
		})
	}
	// Choice telemetry is public like the menu and grants no management access.
	mux.HandleFunc("/boot/choice", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		ids := query["id"]
		if err != nil || len(ids) != 1 {
			http.Error(w, "invalid installation ID", http.StatusBadRequest)
			return
		}
		id, err := strconv.ParseInt(ids[0], 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != ids[0] {
			http.Error(w, "invalid installation ID", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("#!ipxe\n"))
		if record != nil {
			record(Event{Kind: "choice", Path: "/boot/choice?id=" + strconv.FormatInt(id, 10), RemoteAddr: r.RemoteAddr, At: time.Now().UTC()})
		}
	})
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
	// The menu reports the machine's SMBIOS (maker, model, serial, UUID) here
	// before it shows itself; the answer is an empty script.
	mux.HandleFunc("/boot/hello", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("#!ipxe\n"))
		if record != nil {
			record(Event{Kind: "hello", Path: helloPath(helloQuery(r.URL.RawQuery)), RemoteAddr: r.RemoteAddr, At: time.Now().UTC()})
		}
	})
	mux.HandleFunc("/assets/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/assets/")
		// A missing file is logged too: a 404 on boot.wim is where an
		// installation stops.
		notFound := func() {
			http.NotFound(w, r)
			if record != nil {
				record(Event{Kind: "asset", Path: r.URL.Path, RemoteAddr: r.RemoteAddr, At: time.Now().UTC(), Status: http.StatusNotFound})
			}
		}
		if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(name, "windows-answers/") && !validWindowsAnswerAsset(name) {
			http.NotFound(w, r)
			return
		}
		autoinstall := validUbuntuAutoinstallAsset(name)
		if (name == "ubuntu-autoinstall" || strings.HasPrefix(name, "ubuntu-autoinstall/")) && !autoinstall {
			notFound()
			return
		}
		private := privateWinPEScript(name) || validWindowsAnswerAsset(name) || autoinstall
		if private {
			client, err := netip.ParseAddrPort(r.RemoteAddr)
			if err != nil || !netip.MustParsePrefix("10.30.0.0/24").Contains(client.Addr()) {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
		}
		candidate := filepath.Join(root, filepath.FromSlash(name))
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			notFound()
			return
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			http.NotFound(w, r)
			return
		}
		// Generic asset aliases must not bypass the NoCloud path validator or
		// subnet policy by resolving into its private namespace.
		resolvedName := filepath.ToSlash(rel)
		if (resolvedName == "ubuntu-autoinstall" || strings.HasPrefix(resolvedName, "ubuntu-autoinstall/")) && (!autoinstall || resolvedName != name) {
			notFound()
			return
		}
		var file *os.File
		if autoinstall {
			file, err = openPublicAutoinstallAsset(root, name)
		} else {
			file, err = openResolvedAsset(root, rel)
		}
		if err != nil {
			notFound()
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			notFound()
			return
		}
		id, live := transfers.start(r.URL.Path, r.RemoteAddr, info.Size())
		sw := &statusWriter{ResponseWriter: w}
		http.ServeContent(sw, r, info.Name(), info.ModTime(), countingFile{File: file, sent: &live.sent})
		transfers.finish(id)
		if record != nil {
			event := Event{Kind: "asset", Path: r.URL.Path, RemoteAddr: r.RemoteAddr, At: time.Now().UTC(), Status: sw.status, Bytes: sw.bytes}
			if r.Method == http.MethodGet {
				event.Size = info.Size()
			}
			record(event)
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ServeMux redirects dot segments and repeated slashes before routing.
		// Reject malformed NoCloud paths here so they get an exact 404 instead.
		name := strings.TrimPrefix(r.URL.Path, "/assets/")
		cleanName := strings.TrimPrefix(path.Clean(r.URL.Path), "/assets/")
		if name == "ubuntu-autoinstall" || strings.HasPrefix(name, "ubuntu-autoinstall/") || cleanName == "ubuntu-autoinstall" || strings.HasPrefix(cleanName, "ubuntu-autoinstall/") {
			if !validUbuntuAutoinstallAsset(name) {
				http.NotFound(w, r)
				return
			}
		}
		mux.ServeHTTP(w, r)
	}), nil
}

func validWindowsAnswerAsset(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 3 || parts[0] != "windows-answers" || (parts[2] != "omb-answer.xml" && parts[2] != "omb-setup.cmd") {
		return false
	}
	id, err := strconv.ParseUint(parts[1], 10, 63)
	return err == nil && id > 0 && strconv.FormatUint(id, 10) == parts[1]
}

func privateWinPEScript(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 3 || parts[0] != "windows-profiles" || parts[2] != "omb-setup.cmd" {
		return false
	}
	id, err := strconv.ParseUint(parts[1], 10, 63)
	return err == nil && id > 0
}

// HelloFields are the machine details the menu reports to /boot/hello.
var HelloFields = []string{"manufacturer", "product", "serial", "uuid", "platform"}

// helloPath keeps the known fields of a hello request, each cut to 128
// characters, as the logged path.
// helloQuery reads the hello report. iPXE's uristring encodes a space as
// %20 and leaves '+' alone, so '+' is a plus sign here, not a space as in
// HTML forms: QEMU's model is "Standard PC (Q35 + ICH9, 2009)".
func helloQuery(raw string) url.Values {
	q, _ := url.ParseQuery(strings.ReplaceAll(raw, "+", "%2B"))
	return q
}

func helloPath(query url.Values) string {
	kept := url.Values{}
	for _, field := range HelloFields {
		value := strings.TrimSpace(query.Get(field))
		if len(value) > 128 {
			value = value[:128]
		}
		if value != "" {
			kept.Set(field, value)
		}
	}
	return "/boot/hello?" + kept.Encode()
}

// Alias resolution happens before the namespace policy check. Open the resolved
// path without following any subsequent symlink substitutions, retaining stable
// aliases while preventing a concurrent update from redirecting this request.
func openResolvedAsset(root, relative string) (*os.File, error) {
	if !filepath.IsLocal(relative) {
		return nil, fmt.Errorf("asset path is outside the root")
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	dir, err := openAutoinstallDirectory(root, parts[:len(parts)-1], false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return openAutoinstallFile(dir, parts[len(parts)-1])
}
