package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// InstallShare is the Samba share WinPE mounts to run Windows Setup.
const InstallShare = "ombisos"

// SMBSession is what the server saw of one WinPE client on the install
// share: when it connected, which profile it opened, when Windows Setup
// started copying the install image and when it let go of it. After the copy
// Setup reboots and the machine reports nothing more.
//
// An open install image alone does not mean copying: Setup opens it already
// to list the editions and keeps it open on the edition and disk screens.
// Copying is when the Samba process serving the client has read CopyStartBytes
// since the image was first opened.
type SMBSession struct {
	ID        string     `json:"id"`
	IP        string     `json:"ip"`
	PID       string     `json:"pid,omitempty"`
	User      string     `json:"user,omitempty"`
	Profile   string     `json:"profile,omitempty"`
	Started   time.Time  `json:"started"`
	LastSeen  time.Time  `json:"last_seen"`
	LastRead  int64      `json:"last_read,omitempty"`
	LastIOAt  *time.Time `json:"last_io_at,omitempty"`
	Setup     bool       `json:"setup,omitempty"`
	WIMOpened *time.Time `json:"wim_opened,omitempty"` // first seen open
	WIMOpen   bool       `json:"wim_open,omitempty"`   // open right now
	WIMSize   int64      `json:"wim_size,omitempty"`
	ReadStart *int64     `json:"read_start,omitempty"` // bytes the Samba process had read when the image was first open
	CopyRead  int64      `json:"copy_read,omitempty"`  // bytes read since then
	Copying   *time.Time `json:"copying,omitempty"`
	Copied    *time.Time `json:"copied,omitempty"` // image released after copying
	Ended     *time.Time `json:"ended,omitempty"`
}

// CopyStartBytes is how much the client must read after opening the install
// image before it counts as copying. Listing the editions reads a few
// megabytes; applying one reads gigabytes.
const CopyStartBytes = 256 << 20

// SMBSessions is the watcher's history. Since is when it first read Samba
// successfully: before that, a missing session means nothing.
type SMBSessions struct {
	Since    time.Time    `json:"since"`
	Sessions []SMBSession `json:"sessions"`
}

// smbSnapshot is one look at the share: who is connected and what they hold
// open, by session ID.
type smbSnapshot struct {
	at       time.Time
	sessions map[string]smbLive
}

type smbLive struct {
	ip, user, profile string
	pid, wimPath      string
	started           time.Time
	setup, wim        bool
}

var smbCreationTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999-0700",
	"2006-01-02T15:04:05-0700",
	"2006-01-02 15:04:05.999999999-0700",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05-0700",
}

// parseSMBCreationTime parses Samba's creation_time across versions:
// Samba 4.19 (Ubuntu 24.04) emits "+0300" without a colon, while Samba 4.23+
// emits RFC3339 "+03:00".
func parseSMBCreationTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range smbCreationTimeLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// parseSMBStatus reads `smbstatus --json` and keeps the sessions connected
// to the install share.
func parseSMBStatus(data []byte, share string, at time.Time) (smbSnapshot, error) {
	type serverID struct {
		PID      string `json:"pid"`
		UniqueID string `json:"unique_id"`
	}
	var raw struct {
		Sessions map[string]struct {
			SessionID     string   `json:"session_id"`
			ServerID      serverID `json:"server_id"`
			Username      string   `json:"username"`
			RemoteMachine string   `json:"remote_machine"`
			CreationTime  string   `json:"creation_time"`
		} `json:"sessions"`
		Tcons map[string]struct {
			Service   string `json:"service"`
			SessionID string `json:"session_id"`
		} `json:"tcons"`
		OpenFiles map[string]struct {
			ServicePath string `json:"service_path"`
			Filename    string `json:"filename"`
			Opens       map[string]struct {
				ServerID serverID `json:"server_id"`
			} `json:"opens"`
		} `json:"open_files"`
	}
	snap := smbSnapshot{at: at, sessions: map[string]smbLive{}}
	if err := json.Unmarshal(data, &raw); err != nil {
		return snap, err
	}
	onShare := map[string]bool{}
	for _, t := range raw.Tcons {
		if t.Service == share {
			onShare[t.SessionID] = true
		}
	}
	byProcess := map[serverID]string{}
	for key, s := range raw.Sessions {
		id := s.SessionID
		if id == "" {
			id = key
		}
		if !onShare[id] {
			continue
		}
		started, _ := parseSMBCreationTime(s.CreationTime)
		snap.sessions[id] = smbLive{ip: s.RemoteMachine, user: s.Username, pid: s.ServerID.PID, started: started}
		byProcess[s.ServerID] = id
	}
	for _, f := range raw.OpenFiles {
		name := strings.ReplaceAll(f.Filename, "\\", "/")
		parts := strings.Split(name, "/")
		if len(parts) < 2 {
			continue
		}
		for _, open := range f.Opens {
			id, ok := byProcess[open.ServerID]
			if !ok {
				continue
			}
			live := snap.sessions[id]
			if live.profile == "" {
				live.profile = parts[0]
			}
			if len(parts) >= 3 && strings.EqualFold(parts[1], "sources") {
				live.setup = true
				if isInstallImage(parts[len(parts)-1]) {
					live.wim = true
					if f.ServicePath != "" {
						live.wimPath = filepath.Join(f.ServicePath, filepath.FromSlash(name))
					}
				}
			}
			snap.sessions[id] = live
		}
	}
	return snap, nil
}

// isInstallImage tells install.wim, install.esd and split install*.swm from
// the Setup files around them.
func isInstallImage(name string) bool {
	name = strings.ToLower(name)
	return strings.HasPrefix(name, "install") && (strings.HasSuffix(name, ".wim") || strings.HasSuffix(name, ".esd") || strings.HasSuffix(name, ".swm"))
}

// SMBWatcher polls Samba and keeps a week of install-share sessions.
type SMBWatcher struct {
	Status   func(context.Context) ([]byte, error)
	Path     string // where history survives an Agent restart; empty keeps it in memory
	Share    string
	Interval time.Duration
	Keep     time.Duration
	Now      func() time.Time
	// ReadBytes is how much a Samba process has read so far; SizeOf is the
	// size of a file. Nil uses /proc and stat.
	ReadBytes    func(pid string) (int64, bool)
	SizeOf       func(path string) int64
	DisconnectFn func(ctx context.Context, ip, pid string) error

	mu       sync.Mutex
	sessions map[string]*SMBSession
	since    time.Time
	loaded   bool
	lastErr  error
}

// NewSMBWatcher watches the install share with smbstatus every 10 seconds.
func NewSMBWatcher(path string) *SMBWatcher {
	return &SMBWatcher{
		Status: func(ctx context.Context) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			// smbstatus warns on stderr inside the Agent sandbox (no network
			// interfaces); the JSON on stdout is complete.
			return exec.CommandContext(ctx, "smbstatus", "--json").Output()
		},
		Path:         path,
		Share:        InstallShare,
		Interval:     10 * time.Second,
		Keep:         7 * 24 * time.Hour,
		DisconnectFn: disconnectSMBClient,
	}
}

func disconnectSMBClient(ctx context.Context, ip, pid string) error {
	if pid != "" {
		if _, err := strconv.Atoi(pid); err != nil {
			return fmt.Errorf("invalid smb pid")
		}
	}
	if ip != "" {
		if _, err := netip.ParseAddr(ip); err != nil {
			return fmt.Errorf("invalid smb ip")
		}
	}
	if pid == "" && ip == "" {
		return fmt.Errorf("pid or ip required")
	}
	var errs []error
	succeeded := false
	if pid != "" {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := exec.CommandContext(cctx, "smbcontrol", pid, "close-share", InstallShare).CombinedOutput()
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("smbcontrol close-share: %v (%s)", err, strings.TrimSpace(string(out))))
		} else {
			succeeded = true
		}
	}
	if ip != "" {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := exec.CommandContext(cctx, "smbcontrol", "smbd", "kill-client-ip", ip).CombinedOutput()
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("smbcontrol kill-client-ip: %v (%s)", err, strings.TrimSpace(string(out))))
		} else {
			succeeded = true
		}
	}
	if !succeeded && len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// procReadBytes reads rchar of a process: everything it read, from disk or
// cache, by any of its threads.
func procReadBytes(pid string) (int64, bool) {
	if pid == "" || strings.ContainsAny(pid, "/.") {
		return 0, false
	}
	data, err := os.ReadFile("/proc/" + pid + "/io")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "rchar:"); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

func fileSize(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.Size()
	}
	return 0
}

func (w *SMBWatcher) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func (w *SMBWatcher) load() {
	if w.loaded {
		return
	}
	w.loaded = true
	w.sessions = map[string]*SMBSession{}
	if w.Path == "" {
		return
	}
	data, err := os.ReadFile(w.Path)
	if err != nil {
		return
	}
	var saved SMBSessions
	if json.Unmarshal(data, &saved) != nil {
		return
	}
	w.since = saved.Since
	for i := range saved.Sessions {
		s := saved.Sessions[i]
		w.sessions[s.ID+"@"+s.Started.Format(time.RFC3339Nano)] = &s
	}
}

// Observe merges one snapshot into the history.
func (w *SMBWatcher) observe(snap smbSnapshot) {
	w.load()
	if w.since.IsZero() {
		w.since = snap.at
	}
	seen := map[string]bool{}
	for id, live := range snap.sessions {
		var key string
		var s *SMBSession
		started := live.started
		if !started.IsZero() {
			key = id + "@" + started.Format(time.RFC3339Nano)
			s = w.sessions[key]
		} else {
			// Fallback when creation_time is missing or in an unknown format:
			// keep updating the already-open session for this ID instead of
			// splitting it into a new session every 10 seconds.
			for k, existing := range w.sessions {
				if existing.Ended == nil && existing.ID == id && (existing.IP == "" || live.ip == "" || existing.IP == live.ip) {
					key = k
					s = existing
					break
				}
			}
			if s == nil {
				started = snap.at.UTC()
				key = id + "@" + started.Format(time.RFC3339Nano)
			}
		}
		seen[key] = true
		if s == nil {
			s = &SMBSession{ID: id, IP: live.ip, User: live.user, Started: started}
			w.sessions[key] = s
		}
		s.LastSeen = snap.at
		s.Ended = nil
		if live.profile != "" {
			s.Profile = live.profile
		}
		if live.setup {
			s.Setup = true
		}
		w.follow(s, live, snap.at)
	}
	for key, s := range w.sessions {
		if !seen[key] && s.Ended == nil {
			at := s.LastSeen
			s.Ended = &at
		}
		if snap.at.Sub(s.LastSeen) > w.Keep {
			delete(w.sessions, key)
		}
	}
}

// follow updates the copy state of a session from one look at it.
func (w *SMBWatcher) follow(s *SMBSession, live smbLive, at time.Time) {
	if live.pid != "" {
		s.PID = live.pid
	}
	s.WIMOpen = live.wim
	if live.wim && s.WIMOpened == nil {
		s.WIMOpened = &at
	}
	if live.wim && s.WIMSize == 0 && live.wimPath != "" {
		sizeOf := w.SizeOf
		if sizeOf == nil {
			sizeOf = fileSize
		}
		s.WIMSize = sizeOf(live.wimPath)
	}
	readBytes := w.ReadBytes
	if readBytes == nil {
		readBytes = procReadBytes
	}
	if read, ok := readBytes(live.pid); ok {
		if s.LastIOAt == nil || read > s.LastRead {
			t := at
			s.LastIOAt = &t
			s.LastRead = read
		}
		if live.wim && s.ReadStart == nil {
			s.ReadStart = &read
		}
		if s.ReadStart != nil && read >= *s.ReadStart {
			s.CopyRead = read - *s.ReadStart
		}
	} else if s.LastIOAt == nil {
		t := s.Started
		s.LastIOAt = &t
	}
	switch {
	case live.wim && s.Copying == nil && s.CopyRead >= CopyStartBytes:
		s.Copying = &at
	case live.wim:
		s.Copied = nil // opened again, for example the next .swm part
	case s.Copying != nil && s.Copied == nil:
		s.Copied = &at
	}
}

func (w *SMBWatcher) list() []SMBSession {
	out := make([]SMBSession, 0, len(w.sessions))
	for _, s := range w.sessions {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

func (w *SMBWatcher) save() error {
	if w.Path == "" {
		return nil
	}
	data, err := json.Marshal(SMBSessions{Since: w.since, Sessions: w.list()})
	if err != nil {
		return err
	}
	tmp := w.Path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(w.Path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, w.Path)
}

// Poll reads Samba once and records what changed.
func (w *SMBWatcher) Poll(ctx context.Context) error {
	if w.Status == nil {
		return errors.New("smbstatus unavailable")
	}
	data, err := w.Status(ctx)
	if err == nil {
		var snap smbSnapshot
		snap, err = parseSMBStatus(data, w.Share, w.now())
		if err == nil {
			w.mu.Lock()
			before, _ := json.Marshal(SMBSessions{Since: w.sinceLocked(), Sessions: w.list()})
			w.observe(snap)
			after, _ := json.Marshal(SMBSessions{Since: w.since, Sessions: w.list()})
			if string(before) != string(after) {
				err = w.save()
			}
			w.mu.Unlock()
		}
	}
	w.mu.Lock()
	w.lastErr = err
	w.mu.Unlock()
	return err
}

func (w *SMBWatcher) sinceLocked() time.Time {
	w.load()
	return w.since
}

// Sessions returns the history, newest first, or the last polling error when
// Samba has never answered.
func (w *SMBWatcher) Sessions() (SMBSessions, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	since := w.sinceLocked()
	if since.IsZero() {
		if w.lastErr == nil {
			return SMBSessions{}, errors.New("smbstatus not read yet")
		}
		return SMBSessions{}, w.lastErr
	}
	return SMBSessions{Since: since, Sessions: w.list()}, nil
}

// Disconnect forcibly closes an active SMB session on the install share and
// refreshes the watcher snapshot.
func (w *SMBWatcher) Disconnect(ctx context.Context, sessionID, ip, pid string) error {
	sessionID = strings.TrimSpace(sessionID)
	ip = strings.TrimSpace(ip)
	pid = strings.TrimSpace(pid)
	w.mu.Lock()
	w.load()
	var matched *SMBSession
	for _, s := range w.sessions {
		if s.Ended != nil {
			continue
		}
		if sessionID != "" && s.ID == sessionID {
			matched = s
			break
		}
		if sessionID == "" && ip != "" && s.IP == ip {
			matched = s
			break
		}
	}
	if matched != nil {
		if ip == "" {
			ip = matched.IP
		}
		if pid == "" {
			pid = matched.PID
		}
	}
	w.mu.Unlock()
	if ip == "" && pid == "" {
		return errors.New("active smb session not found")
	}
	fn := w.DisconnectFn
	if fn == nil {
		fn = disconnectSMBClient
	}
	if err := fn(ctx, ip, pid); err != nil {
		return err
	}
	if w.Status != nil {
		_ = w.Poll(ctx)
	}
	return nil
}

// ClearEnded forgets the sessions that are over; the ones still connected
// stay, so an installation in progress keeps its state.
func (w *SMBWatcher) ClearEnded() (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.load()
	n := 0
	for key, s := range w.sessions {
		if s.Ended != nil {
			delete(w.sessions, key)
			n++
		}
	}
	return n, w.save()
}

// Run polls until ctx ends, logging only when polling starts or stops
// failing.
func (w *SMBWatcher) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	failing := false
	for {
		err := w.Poll(ctx)
		switch {
		case err != nil && !failing:
			log.Printf("SMB session watcher: %v", err)
			failing = true
		case err == nil && failing:
			log.Printf("SMB session watcher: smbstatus answers again")
			failing = false
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// HandlerWithSMBSessions adds GET /smb-sessions to the Agent API.
func HandlerWithSMBSessions(inner http.Handler, watcher *SMBWatcher) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", inner)
	mux.HandleFunc("POST /smb-sessions/disconnect", func(rw http.ResponseWriter, r *http.Request) {
		if watcher == nil {
			http.Error(rw, "smb sessions unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			ID  string `json:"id"`
			IP  string `json:"ip"`
			PID string `json:"pid"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(rw, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := watcher.Disconnect(r.Context(), req.ID, req.IP, req.PID); err != nil {
			status := http.StatusBadGateway
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			} else if strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "required") {
				status = http.StatusBadRequest
			}
			http.Error(rw, err.Error(), status)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(map[string]bool{"disconnected": true})
	})
	mux.HandleFunc("POST /smb-sessions/clear", func(rw http.ResponseWriter, _ *http.Request) {
		if watcher == nil {
			http.Error(rw, "smb sessions unavailable", http.StatusServiceUnavailable)
			return
		}
		n, err := watcher.ClearEnded()
		if err != nil {
			http.Error(rw, "smb sessions unavailable", http.StatusServiceUnavailable)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(map[string]int{"deleted": n})
	})
	mux.HandleFunc("GET /smb-sessions", func(rw http.ResponseWriter, _ *http.Request) {
		if watcher == nil {
			http.Error(rw, "smb sessions unavailable", http.StatusServiceUnavailable)
			return
		}
		list, err := watcher.Sessions()
		if err != nil {
			http.Error(rw, "smb sessions unavailable", http.StatusServiceUnavailable)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(list)
	})
	return mux
}
