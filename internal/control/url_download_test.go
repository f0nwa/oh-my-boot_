package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"omb/internal/images"
)

func TestIsBlockedDownloadIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "127.10.20.30", "::1", "169.254.169.254", "fe80::1", "0.0.0.0", "::", "224.0.0.1", "ff02::1"}
	for _, s := range blocked {
		if !IsBlockedDownloadIP(net.ParseIP(s)) {
			t.Errorf("expected %s to be blocked", s)
		}
	}
	allowed := []string{"192.168.10.50", "10.0.0.5", "172.16.1.20", "91.189.91.123", "2606:2800:220:1:248:1893:25c8:1946"}
	for _, s := range allowed {
		if IsBlockedDownloadIP(net.ParseIP(s)) {
			t.Errorf("expected %s to be allowed", s)
		}
	}
}

func TestURLDownloadClientBlocksLoopback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := NewURLDownloadHTTPClient(false, 5*time.Second, nil)
	_, err := ProbeURL(context.Background(), client, ts.URL+"/ubuntu.iso")
	if err == nil {
		t.Fatalf("expected SSRF error for loopback test server")
	}
}

func TestProbeURLHeadAndRangeFallback(t *testing.T) {
	payload := []byte("0123456789abcdef")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/head-ok":
			w.Header().Set("Content-Disposition", `attachment; filename="ubuntu-24.04.iso"`)
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			w.WriteHeader(http.StatusOK)
		case "/no-head":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''debian-12.iso")
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
			w.Header().Set("Content-Length", "1")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[:1])
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	client := NewURLDownloadHTTPClient(false, 5*time.Second, func(net.IP) bool { return false })

	res1, err := ProbeURL(context.Background(), client, ts.URL+"/head-ok")
	if err != nil || res1.Filename != "ubuntu-24.04.iso" || res1.Size != int64(len(payload)) || res1.StatusCode != 200 {
		t.Fatalf("head-ok probe = %+v, err=%v", res1, err)
	}

	res2, err := ProbeURL(context.Background(), client, ts.URL+"/no-head")
	if err != nil || res2.Filename != "debian-12.iso" || res2.Size != int64(len(payload)) || res2.StatusCode != 206 {
		t.Fatalf("no-head probe = %+v, err=%v", res2, err)
	}
}

type fakeStreamImagesAPI struct {
	fakeImagesAPI
	mu           sync.Mutex
	lastOpts     images.ReceiveOptions
	lastSize     int64
	lastFilename string
	lastData     []byte
	failStatus   int
	blockCh      chan struct{}
}

func (f *fakeStreamImagesAPI) UploadImageWithOptions(ctx context.Context, id int64, folder, name, expectedKind string, body io.Reader, size int64, opts images.ReceiveOptions) (images.UploadResult, error) {
	if f.blockCh != nil {
		select {
		case <-ctx.Done():
			return images.UploadResult{}, ctx.Err()
		case <-f.blockCh:
		}
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return images.UploadResult{}, err
	}
	if f.failStatus != 0 {
		return images.UploadResult{}, fmt.Errorf("image upload: %d simulated error", f.failStatus)
	}
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])
	if opts.ChecksumAlgo == "sha256" && opts.ChecksumValue != hexSum {
		return images.UploadResult{}, fmt.Errorf("image upload: 422 checksum mismatch")
	}
	f.mu.Lock()
	f.lastOpts = opts
	f.lastSize = size
	f.lastFilename = name
	f.lastData = data
	f.mu.Unlock()
	rel := name
	if folder != "" {
		rel = folder + "/" + name
	}
	return images.UploadResult{Path: rel, Kind: expectedKind, Size: int64(len(data)), SHA256: hexSum}, nil
}

func TestURLDownloadEndToEndChecksumAndCancel(t *testing.T) {
	payload := []byte("fake-iso-binary-stream-content-1234567890")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow.iso" {
			w.Header().Set("Content-Length", "1048576")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer remote.Close()

	localSt := images.Storage{ID: 1, Name: "Local", Kind: "local", Root: "/var/lib/omb-images/iso", State: "ready", Uploads: true, Total: 100 << 30, Free: 50 << 30}
	api := &fakeStreamImagesAPI{fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{localSt}}}}
	s := &Server{
		Store:  authOnlyStore{user: User{ID: 2, Username: "op", Role: "operator", Enabled: true}},
		Images: api,
		urlDownloadClient: func(insecureTLS bool, timeout time.Duration) *http.Client {
			return NewURLDownloadHTTPClient(insecureTLS, timeout, func(net.IP) bool { return false })
		},
	}
	h := s.Handler()

	// 1. Start valid download with matching SHA-256
	body, _ := json.Marshal(map[string]any{
		"url":            remote.URL + "/ubuntu.iso",
		"filename":       "ubuntu.iso",
		"storage_id":     1,
		"folder":         "linux",
		"checksum_algo":  "sha256",
		"checksum_value": hexSum,
	})
	req := httptest.NewRequest(http.MethodPost, "/images/url/download", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", CSRFToken("test-token"))
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Wait for completion
	deadline := time.Now().Add(3 * time.Second)
	var doneJob *URLDownloadJob
	for time.Now().Before(deadline) {
		listReq := httptest.NewRequest(http.MethodGet, "/images/url/downloads", nil)
		listReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		listRec := httptest.NewRecorder()
		h.ServeHTTP(listRec, listReq)
		var out struct {
			Jobs []URLDownloadJob `json:"jobs"`
		}
		_ = json.Unmarshal(listRec.Body.Bytes(), &out)
		if len(out.Jobs) == 1 && out.Jobs[0].State != "running" {
			doneJob = &out.Jobs[0]
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if doneJob == nil || doneJob.State != "done" || !doneJob.ChecksumVerified || doneJob.Path != "linux/ubuntu.iso" {
		t.Fatalf("expected done verified job, got %+v", doneJob)
	}

	// 2. Dismiss completed job
	disReq := httptest.NewRequest(http.MethodPost, "/images/url/downloads/"+doneJob.ID+"/dismiss", nil)
	disReq.Header.Set("X-CSRF-Token", CSRFToken("test-token"))
	disReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	disRec := httptest.NewRecorder()
	h.ServeHTTP(disRec, disReq)
	if disRec.Code != http.StatusOK {
		t.Fatalf("dismiss status=%d body=%s", disRec.Code, disRec.Body.String())
	}

	// 3. Start slow download and cancel it
	slowBody, _ := json.Marshal(map[string]any{
		"url":        remote.URL + "/slow.iso",
		"filename":   "slow.iso",
		"storage_id": 1,
	})
	slowReq := httptest.NewRequest(http.MethodPost, "/images/url/download", bytes.NewReader(slowBody))
	slowReq.Header.Set("Content-Type", "application/json")
	slowReq.Header.Set("X-CSRF-Token", CSRFToken("test-token"))
	slowReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	slowRec := httptest.NewRecorder()
	h.ServeHTTP(slowRec, slowReq)
	if slowRec.Code != http.StatusAccepted {
		t.Fatalf("slow start status=%d body=%s", slowRec.Code, slowRec.Body.String())
	}
	var startResp struct {
		Job URLDownloadJob `json:"job"`
	}
	_ = json.Unmarshal(slowRec.Body.Bytes(), &startResp)

	canReq := httptest.NewRequest(http.MethodPost, "/images/url/downloads/"+startResp.Job.ID+"/cancel", nil)
	canReq.Header.Set("X-CSRF-Token", CSRFToken("test-token"))
	canReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	canRec := httptest.NewRecorder()
	h.ServeHTTP(canRec, canReq)
	if canRec.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", canRec.Code, canRec.Body.String())
	}
}

func TestURLDownloadForbiddenForObserver(t *testing.T) {
	s := &Server{Store: authOnlyStore{user: User{ID: 3, Role: "observer", Enabled: true}}, Images: &fakeImagesAPI{}}
	h := s.Handler()
	req := httptest.NewRequest(http.MethodPost, "/images/url/probe", strings.NewReader(`{"url":"https://example.com/a.iso"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", CSRFToken("test-token"))
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for observer, got %d", rec.Code)
	}

	// Observer can still view active URL downloads
	listReq := httptest.NewRequest(http.MethodGet, "/images/url/downloads", nil)
	listReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for observer list, got %d", listRec.Code)
	}
}

func TestImagesPageURLTabRenderedOnlyOnISOTab(t *testing.T) {
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{uploadStorage()}}}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, Images: api}
	h := s.Handler()

	// ISO tab: must have dialog-tabs, url mode pane, probe button, url-downloads-panel, no <details>
	reqISO := httptest.NewRequest(http.MethodGet, "/images", nil)
	reqISO.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	recISO := httptest.NewRecorder()
	h.ServeHTTP(recISO, reqISO)
	htmlISO := recISO.Body.String()
	for _, want := range []string{`data-upload-mode="url"`, `id="iso-url-probe"`, `id="url-downloads-panel"`, `id="iso-url-algo"`} {
		if !strings.Contains(htmlISO, want) {
			t.Errorf("ISO tab missing %q", want)
		}
	}
	if strings.Contains(htmlISO, "<details") || strings.Contains(htmlISO, "<summary") {
		t.Errorf("ISO tab must not use <details>/<summary>")
	}

	// boot.wim and install.wim tabs: must NOT have URL mode switcher or url-downloads-panel
	for _, tab := range []string{"boot", "install"} {
		req := httptest.NewRequest(http.MethodGet, "/images?tab="+tab, nil)
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		html := rec.Body.String()
		if strings.Contains(html, `data-upload-mode="url"`) || strings.Contains(html, `id="url-downloads-panel"`) {
			t.Errorf("tab %s should not render ISO URL download UI", tab)
		}
	}
}
