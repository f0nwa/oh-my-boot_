package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSystemMetricsAPI(t *testing.T) {
	fsys := fstest.MapFS{
		"proc/stat": &fstest.MapFile{
			Data: []byte("cpu  1000 0 500 8500 0 0 0 0\ncpu0 500 0 250 4250 0 0 0 0\ncpu1 500 0 250 4250 0 0 0 0\n"),
		},
		"proc/meminfo": &fstest.MapFile{
			Data: []byte("MemTotal:        8388608 kB\nMemFree:         2097152 kB\nBuffers:          524288 kB\nCached:          3670016 kB\nSReclaimable:     524288 kB\nShmem:                 0 kB\n"),
		},
		"proc/net/dev": &fstest.MapFile{
			Data: []byte("Inter-|   Receive                                                |  Transmit\n face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n ens19: 104857600 100 0 0 0 0 0 0 52428800 100 0 0 0 0 0 0\n"),
		},
		"proc/loadavg": &fstest.MapFile{
			Data: []byte("0.18 0.22 0.15 1/100 123\n"),
		},
		"proc/uptime": &fstest.MapFile{
			Data: []byte("90000.0 180000.0\n"),
		},
	}

	s := &Server{
		Store:  authOnlyStore{user: User{ID: 2, Username: "obs", Role: "observer", Enabled: true}},
		SysMon: NewSystemMonitor(fsys),
	}
	handler := s.Handler()

	// Unauthenticated request should redirect to /login
	unauthReq := httptest.NewRequest(http.MethodGet, "/api/system/metrics", nil)
	unauthRes := httptest.NewRecorder()
	handler.ServeHTTP(unauthRes, unauthReq)
	if unauthRes.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for unauthenticated request, got %d", unauthRes.Code)
	}

	// Authenticated observer request (RU)
	req := httptest.NewRequest(http.MethodGet, "/api/system/metrics", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", res.Code, res.Body.String())
	}
	if ct := res.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected application/json Content-Type, got %q", ct)
	}

	var snap SystemSnapshot
	if err := json.Unmarshal(res.Body.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !snap.Available || snap.CPUCores != 2 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if snap.UptimeHuman != "1 дн 1 ч" {
		t.Fatalf("unexpected RU uptime: %q", snap.UptimeHuman)
	}
	if snap.MemTotalHuman != "8.0 ГБ" {
		t.Fatalf("unexpected RU MemTotalHuman: %q", snap.MemTotalHuman)
	}

	// Authenticated request in English
	sEN := &Server{
		Store:  authOnlyStore{user: User{ID: 2, Username: "obs", Role: "observer", Enabled: true, Language: langEN}},
		SysMon: NewSystemMonitor(fsys),
	}
	reqEN := httptest.NewRequest(http.MethodGet, "/api/system/metrics", nil)
	reqEN.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	resEN := httptest.NewRecorder()
	sEN.Handler().ServeHTTP(resEN, reqEN)
	if resEN.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for EN, got %d", resEN.Code)
	}
	var snapEN SystemSnapshot
	if err := json.Unmarshal(resEN.Body.Bytes(), &snapEN); err != nil {
		t.Fatalf("unmarshal EN response: %v", err)
	}
	if snapEN.UptimeHuman != "1 d 1 h" {
		t.Fatalf("unexpected EN uptime: %q", snapEN.UptimeHuman)
	}
	if snapEN.MemTotalHuman != "8.0 GB" {
		t.Fatalf("unexpected EN MemTotalHuman: %q", snapEN.MemTotalHuman)
	}
}

func TestSystemScript(t *testing.T) {
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	req := httptest.NewRequest(http.MethodGet, "/assets/system.js", nil)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /assets/system.js, got %d", res.Code)
	}
	if ct := res.Header().Get("Content-Type"); !strings.Contains(ct, "application/javascript") {
		t.Fatalf("expected application/javascript Content-Type, got %q", ct)
	}
	body := res.Body.String()
	for _, want := range []string{"/api/system/metrics", "visibilitychange", "mon-cpu-fill", "mon-mem-fill-used", "mon-net-rx-rate"} {
		if !strings.Contains(body, want) {
			t.Fatalf("/assets/system.js lacks %q", want)
		}
	}
}

func TestSystemUIRender(t *testing.T) {
	fsys := fstest.MapFS{
		"proc/stat": &fstest.MapFile{
			Data: []byte("cpu  1000 0 500 8500 0 0 0 0\ncpu0 250 0 125 2125 0 0 0 0\ncpu1 250 0 125 2125 0 0 0 0\ncpu2 250 0 125 2125 0 0 0 0\ncpu3 250 0 125 2125 0 0 0 0\n"),
		},
		"proc/meminfo": &fstest.MapFile{
			Data: []byte("MemTotal:        8388608 kB\nMemFree:         2097152 kB\nBuffers:          524288 kB\nCached:          3670016 kB\nSReclaimable:     524288 kB\nShmem:                 0 kB\n"),
		},
		"proc/net/dev": &fstest.MapFile{
			Data: []byte("Inter-|   Receive                                                |  Transmit\n face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n ens19: 15032385536 100 0 0 0 0 0 0 195957882880 100 0 0 0 0 0 0\n"),
		},
		"proc/loadavg": &fstest.MapFile{
			Data: []byte("0.18 0.22 0.15 1/100 123\n"),
		},
		"proc/uptime": &fstest.MapFile{
			Data: []byte("1231200.0 2400000.0\n"),
		},
	}

	s := &Server{
		Store:  usersStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true}}},
		SysMon: NewSystemMonitor(fsys),
	}
	bodyRU := libraryGet(t, s, "/system")
	for _, want := range []string{
		`id="server-resources-card"`,
		"Ресурсы сервера",
		"все интерфейсы",
		"Процессор",
		"4 ядер",
		"LA: 0.18 · 0.22 · 0.15",
		"Память",
		`id="mon-mem-fill-used"`,
		`id="mon-mem-fill-cache"`,
		"проц",
		"кэш",
		"Свободно",
		"Скорость сети",
		"Всего передано",
		`id="mon-net-rx-rate"`,
		`id="mon-net-tx-rate"`,
		"Аптайм",
		"14 дн 6 ч",
		`<script defer src="/assets/system.js"></script>`,
	} {
		if !strings.Contains(bodyRU, want) {
			t.Fatalf("RU /system page lacks %q: %s", want, bodyRU)
		}
	}

	sEN := &Server{
		Store:  authOnlyStore{user: User{ID: 2, Username: "obs", Role: "observer", Enabled: true, Language: langEN}},
		SysMon: NewSystemMonitor(fsys),
	}
	bodyEN := libraryGet(t, sEN, "/system")
	for _, want := range []string{
		"Server resources",
		"all interfaces",
		"CPU",
		"4 cores",
		"Memory",
		"proc",
		"cache",
		"Free",
		"Network speed",
		"Total transferred",
		"Uptime",
		"14 d 6 h",
	} {
		if !strings.Contains(bodyEN, want) {
			t.Fatalf("EN /system page lacks %q: %s", want, bodyEN)
		}
	}

	cssReq := httptest.NewRequest(http.MethodGet, "/assets/style.css", nil)
	cssRes := httptest.NewRecorder()
	s.Handler().ServeHTTP(cssRes, cssReq)
	css := cssRes.Body.String()
	for _, want := range []string{".resource-grid", ".meter-track", ".fill-cache", ".net-rates"} {
		if !strings.Contains(css, want) {
			t.Fatalf("style.css lacks %q", want)
		}
	}
}


