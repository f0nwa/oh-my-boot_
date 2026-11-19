package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const mb = 1 << 20

func smbStatusJSON(withSession bool, files ...string) []byte {
	v := map[string]any{"sessions": map[string]any{}, "tcons": map[string]any{}, "open_files": map[string]any{}}
	if !withSession {
		b, _ := json.Marshal(v)
		return b
	}
	sid := map[string]any{"pid": "58474", "unique_id": "119"}
	v["sessions"] = map[string]any{"1002": map[string]any{"session_id": "1002", "server_id": sid, "username": "omb-iso", "remote_machine": "10.30.0.19", "creation_time": "2026-10-23T10:04:17.243763+03:00"}}
	v["tcons"] = map[string]any{"35": map[string]any{"service": "ombisos", "session_id": "1002"}, "36": map[string]any{"service": "other", "session_id": "9"}}
	open := map[string]any{}
	for _, f := range files {
		open["/mnt/omb-iso/profiles/"+f] = map[string]any{"service_path": "/mnt/omb-iso/profiles", "filename": f, "opens": map[string]any{"58474/1": map[string]any{"server_id": sid}}}
	}
	v["open_files"] = open
	b, _ := json.Marshal(v)
	return b
}

// setupFiles is what Windows Setup holds open from the start.
var setupFiles = []string{"2/sources/setup.exe", "2/sources/uxlib.dll", "2/sources/wimgapi.dll"}

func withWIM() []string { return append(append([]string{}, setupFiles...), "2/sources/install.wim") }

func TestParseSMBStatusKeepsInstallShare(t *testing.T) {
	snap, err := parseSMBStatus(smbStatusJSON(true, withWIM()...), "ombisos", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	live, ok := snap.sessions["1002"]
	if !ok || len(snap.sessions) != 1 {
		t.Fatalf("sessions = %#v", snap.sessions)
	}
	if live.ip != "10.30.0.19" || live.profile != "2" || !live.setup || !live.wim || live.pid != "58474" || live.wimPath != "/mnt/omb-iso/profiles/2/sources/install.wim" {
		t.Fatalf("live = %#v", live)
	}
	if got := live.started.Format(time.RFC3339); got != "2026-10-23T07:04:17Z" {
		t.Fatalf("started = %s", got)
	}
	if isInstallImage("boot.wim") || !isInstallImage("install2.swm") || !isInstallImage("INSTALL.ESD") {
		t.Fatal("isInstallImage")
	}
}

// setupRun drives a watcher through polls: each step sets what Samba shows
// and how much the serving process has read in total.
type setupRun struct {
	t      *testing.T
	w      *SMBWatcher
	now    time.Time
	status []byte
	read   int64
}

func newSetupRun(t *testing.T, path string) *setupRun {
	r := &setupRun{t: t, now: time.Date(2026, 10, 23, 7, 5, 0, 0, time.UTC)}
	r.w = &SMBWatcher{
		Path: path, Share: "ombisos", Keep: 7 * 24 * time.Hour,
		Now:       func() time.Time { return r.now },
		Status:    func(context.Context) ([]byte, error) { return r.status, nil },
		ReadBytes: func(pid string) (int64, bool) { return r.read, pid == "58474" },
		SizeOf:    func(string) int64 { return 8693893898 },
	}
	return r
}

func (r *setupRun) step(read int64, connected bool, files ...string) SMBSession {
	r.t.Helper()
	r.status, r.read, r.now = smbStatusJSON(connected, files...), read, r.now.Add(10*time.Second)
	if err := r.w.Poll(context.Background()); err != nil {
		r.t.Fatal(err)
	}
	got, err := r.w.Sessions()
	if err != nil || len(got.Sessions) != 1 {
		r.t.Fatalf("sessions = %#v, %v", got, err)
	}
	return got.Sessions[0]
}

// The run seen on the test VM on 23.10.2026: Setup opened install.wim to
// list the editions, closed it, and opened it again on the edition screen,
// all before anything was copied.
func TestSMBWatcherDoesNotTakeTheEditionListForACopy(t *testing.T) {
	r := newSetupRun(t, "")
	s := r.step(20*mb, true, setupFiles...)
	if !s.Setup || s.WIMOpened != nil || s.Copying != nil {
		t.Fatalf("Setup started: %#v", s)
	}
	s = r.step(30*mb, true, withWIM()...)
	if s.WIMOpened == nil || !s.WIMOpen || s.Copying != nil || s.WIMSize != 8693893898 {
		t.Fatalf("editions listed: %#v", s)
	}
	s = r.step(49*mb, true, setupFiles...)
	if s.Copying != nil || s.Copied != nil || s.WIMOpen {
		t.Fatalf("install.wim closed after the edition list must not count as copied: %#v", s)
	}
	for i := 0; i < 30; i++ { // the person reads the edition and disk screens
		s = r.step(49*mb+int64(i)*mb, true, withWIM()...)
	}
	if s.Copying != nil || s.Copied != nil {
		t.Fatalf("waiting on the disk screen: %#v", s)
	}
}

func TestSMBWatcherFollowsACopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smb-sessions.json")
	r := newSetupRun(t, path)
	r.step(20*mb, true, setupFiles...)
	r.step(30*mb, true, withWIM()...)
	s := r.step(30*mb+CopyStartBytes+mb, true, withWIM()...)
	if s.Copying == nil || s.Copied != nil {
		t.Fatalf("copy started: %#v", s)
	}
	s = r.step(4<<30, true, withWIM()...)
	if s.Copied != nil || s.CopyRead != 4<<30-30*mb {
		t.Fatalf("copying: %#v", s)
	}
	s = r.step(5<<30, true, setupFiles...)
	if s.Copied == nil || s.Ended != nil {
		t.Fatalf("copied: %#v", s)
	}
	s = r.step(5<<30, false)
	if s.Ended == nil || s.Copied == nil {
		t.Fatalf("rebooted: %#v", s)
	}
	// History survives a restart.
	again := &SMBWatcher{Path: path}
	got, err := again.Sessions()
	if err != nil || got.Since.IsZero() || len(got.Sessions) != 1 || got.Sessions[0].Copied == nil || got.Sessions[0].CopyRead != 5<<30-30*mb {
		t.Fatalf("reloaded = %#v, %v", got, err)
	}
}

func TestSMBWatcherSeesACopyCutShort(t *testing.T) {
	r := newSetupRun(t, "")
	r.step(30*mb, true, withWIM()...)
	r.step(2<<30, true, withWIM()...)
	s := r.step(2<<30, false)
	if s.Copying == nil || s.Copied != nil || s.Ended == nil {
		t.Fatalf("cut short: %#v", s)
	}
}

func TestSMBWatcherWithoutReadCountsNeverClaimsACopy(t *testing.T) {
	r := newSetupRun(t, "")
	r.w.ReadBytes = func(string) (int64, bool) { return 0, false }
	r.step(0, true, withWIM()...)
	s := r.step(0, true, setupFiles...)
	if s.Copying != nil || s.Copied != nil {
		t.Fatalf("no read counts: %#v", s)
	}
}

func TestSMBWatcherForgetsOldSessions(t *testing.T) {
	r := newSetupRun(t, "")
	r.w.Keep = time.Hour
	r.step(0, true, setupFiles...)
	r.step(0, false)
	r.now = r.now.Add(2 * time.Hour)
	_ = r.w.Poll(context.Background())
	if got, _ := r.w.Sessions(); len(got.Sessions) != 0 {
		t.Fatalf("old sessions kept: %#v", got.Sessions)
	}
}

func TestProcReadBytesRejectsPaths(t *testing.T) {
	if _, ok := procReadBytes("../1"); ok {
		t.Fatal("path accepted")
	}
	if _, ok := procReadBytes("self"); !ok {
		t.Skip("/proc/self/io unreadable here")
	}
}

func TestSMBSessionsHandler(t *testing.T) {
	never := HandlerWithSMBSessions(http.NotFoundHandler(), &SMBWatcher{})
	rec0 := httptest.NewRecorder()
	never.ServeHTTP(rec0, httptest.NewRequest("GET", "/smb-sessions", nil))
	if rec0.Code != http.StatusServiceUnavailable {
		t.Fatalf("unread watcher answered %d", rec0.Code)
	}
	w := &SMBWatcher{Share: "ombisos", Status: func(context.Context) ([]byte, error) { return smbStatusJSON(true), nil }}
	_ = w.Poll(context.Background())
	h := HandlerWithSMBSessions(http.NotFoundHandler(), w)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/smb-sessions", nil))
	var got SMBSessions
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || len(got.Sessions) != 1 || got.Sessions[0].IP != "10.30.0.19" {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestSMBWatcherClearKeepsConnectedSessions(t *testing.T) {
	r := newSetupRun(t, filepath.Join(t.TempDir(), "s.json"))
	r.step(0, true, setupFiles...)
	r.step(0, false) // the first session ends
	r.now = r.now.Add(time.Hour)
	other := smbStatusJSON(true, setupFiles...)
	// A new session: same ID, later start.
	other = []byte(strings.Replace(string(other), "2026-10-23T10:04:17.243763+03:00", "2026-10-23T11:30:00+03:00", 1))
	r.status = other
	_ = r.w.Poll(context.Background())
	h := HandlerWithSMBSessions(http.NotFoundHandler(), r.w)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/smb-sessions/clear", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deleted":1`) {
		t.Fatalf("clear=%d %s", rec.Code, rec.Body)
	}
	got, _ := r.w.Sessions()
	if len(got.Sessions) != 1 || got.Sessions[0].Ended != nil {
		t.Fatalf("left=%+v", got.Sessions)
	}
}

func TestSMBWatcherTracksPIDLastIOAndDisconnects(t *testing.T) {
	r := newSetupRun(t, filepath.Join(t.TempDir(), "s.json"))
	s1 := r.step(300<<20, true, withWIM()...)
	if s1.PID != "58474" || s1.LastIOAt == nil {
		t.Fatalf("expected PID and LastIOAt, got %+v", s1)
	}
	firstIO := *s1.LastIOAt

	// Advance time with no read growth: LastIOAt stays frozen while LastSeen advances.
	r.now = r.now.Add(10 * time.Minute)
	s2 := r.step(300<<20, true, withWIM()...)
	if s2.LastIOAt == nil || !s2.LastIOAt.Equal(firstIO) {
		t.Fatalf("LastIOAt should not advance when bytes do not grow: got %v want %v", s2.LastIOAt, firstIO)
	}

	// Disconnect closes the session via DisconnectFn and refreshes the snapshot.
	var disconnectedIP, disconnectedPID string
	r.w.DisconnectFn = func(_ context.Context, ip, pid string) error {
		disconnectedIP, disconnectedPID = ip, pid
		r.status = smbStatusJSON(false)
		return nil
	}
	h := HandlerWithSMBSessions(http.NotFoundHandler(), r.w)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/smb-sessions/disconnect", strings.NewReader(`{"id":"1002"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"disconnected":true`) {
		t.Fatalf("disconnect response=%d %s", rec.Code, rec.Body.String())
	}
	if disconnectedIP != "10.30.0.19" || disconnectedPID != "58474" {
		t.Fatalf("disconnect args ip=%q pid=%q", disconnectedIP, disconnectedPID)
	}
	got, _ := r.w.Sessions()
	if len(got.Sessions) != 1 || got.Sessions[0].Ended == nil {
		t.Fatalf("session should be marked ended after disconnect: %+v", got.Sessions)
	}
}
