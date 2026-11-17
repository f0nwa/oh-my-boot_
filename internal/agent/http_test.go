package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerConfigShowsOnlySuccessfulApply(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{}
	a, err := NewApplier(filepath.Join(dir, "dnsmasq.conf"), filepath.Join(dir, "state.json"), filepath.Join(dir, "dhcp", "hosts"), runner)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(a)
	read := func() *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest("GET", "/config", nil))
		return res
	}
	if got := read(); got.Code != http.StatusNotFound {
		t.Fatalf("before apply: %d", got.Code)
	}
	original := agentConfig()
	if err := a.Apply(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got.Code != http.StatusOK {
		t.Fatalf("after apply: %d", got.Code)
	}
	var current struct {
		Interface string `json:"interface"`
		Mode      string `json:"mode"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	if current.Interface != original.Interface || current.Mode != original.Mode {
		t.Fatalf("current=%+v", current)
	}
	runner.failOnce = true
	changed := original
	changed.Interface = "enp9s0"
	if err := a.Apply(context.Background(), changed); err == nil {
		t.Fatal("expected restart failure")
	}
	got = read()
	if got.Code != http.StatusOK {
		t.Fatalf("after failure: %d", got.Code)
	}
	if err := json.Unmarshal(got.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	if current.Interface != original.Interface {
		t.Fatalf("failed apply changed state: %+v", current)
	}
}

func TestHandlerRejectsMalformedApplyAndReportsStatus(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{}
	a, err := NewApplier(filepath.Join(dir, "dnsmasq.conf"), filepath.Join(dir, "state.json"), filepath.Join(dir, "dhcp", "hosts"), runner)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(a)
	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/apply", strings.NewReader("{")))
	if bad.Code != http.StatusBadRequest || runner.restarts != 0 {
		t.Fatalf("malformed apply was not rejected: status=%d restarts=%d", bad.Code, runner.restarts)
	}

	payload, err := json.Marshal(agentConfig())
	if err != nil {
		t.Fatal(err)
	}
	good := httptest.NewRecorder()
	h.ServeHTTP(good, httptest.NewRequest(http.MethodPost, "/apply", bytes.NewReader(payload)))
	if good.Code != http.StatusOK || runner.restarts != 1 {
		t.Fatalf("valid apply failed: status=%d body=%q", good.Code, good.Body.String())
	}
	status := httptest.NewRecorder()
	h.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"mode":"proxy"`) || strings.Contains(status.Body.String(), dir) {
		t.Fatalf("unsafe status response: status=%d body=%q", status.Code, status.Body.String())
	}
}

func TestHandlerWithNFSEndpoints(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("inner"))
	})

	// When nfs is nil, all endpoints return 503
	nilHandler := HandlerWithNFS(inner, nil)
	for _, route := range []string{"/nfs/mount", "/nfs/enable-upload", "/nfs/check-upload", "/nfs/remove"} {
		rec := httptest.NewRecorder()
		nilHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{}`)))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s nil nfs: expected 503, got %d", route, rec.Code)
		}
	}

	// Inner fallback
	rec := httptest.NewRecorder()
	nilHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "inner" {
		t.Fatalf("fallback: code=%d body=%s", rec.Code, rec.Body.String())
	}

	base := t.TempDir()
	configDir := filepath.Join(base, "nfs")
	unitDir := filepath.Join(base, "units")
	mountRoot := filepath.Join(base, "mnt-nfs")
	uploadRoot := filepath.Join(base, "mnt-nfs-upload")
	for _, d := range []string{configDir, unitDir, mountRoot, uploadRoot} {
		_ = os.MkdirAll(d, 0755)
	}
	ctl := &fakeSMBSystemctl{}
	nfs := &NFSManager{
		ConfigDir:  configDir,
		UnitDir:    unitDir,
		MountRoot:  mountRoot,
		UploadRoot: uploadRoot,
		ImagesUID:  "1001",
		ImagesGID:  "1001",
		Systemctl:  ctl,
		Probe:      func(string) error { return nil },
		WriteProbe: func(string, string, string) error { return nil },
	}
	handler := HandlerWithNFS(inner, nfs)

	// POST /nfs/mount invalid requests
	badReqs := []string{
		`{`,
		`{"key":"bad"}`,
		`{"key":"0123456789abcdef0123456789abcdef","server":"10.0.0.1","export_path":"/exp","version":"5.0"}`,
		`{"key":"0123456789abcdef0123456789abcdef","server":"10.0.0.1","export_path":"/exp","version":"4.1","extra":true}`,
	}
	for _, b := range badReqs {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/nfs/mount", strings.NewReader(b)))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("bad /nfs/mount: expected 400, got %d", res.Code)
		}
	}

	// POST /nfs/mount valid request
	validMountBody := `{"key":"0123456789abcdef0123456789abcdef","server":"10.0.0.1","export_path":"/exp","version":"4.1"}`
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/nfs/mount", strings.NewReader(validMountBody)))
	if res.Code != http.StatusOK {
		t.Fatalf("valid /nfs/mount: expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"writable":true`) {
		t.Fatalf("expected writable:true in %s", res.Body.String())
	}

	// POST /nfs/enable-upload
	badUploadReqs := []string{
		`{`,
		`{"key":"0123456789abcdef0123456789abcdef","server":"10.0.0.2","export_path":"/exp","version":"4.1"}`, // mismatched server
	}
	for _, b := range badUploadReqs {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/nfs/enable-upload", strings.NewReader(b)))
		if res.Code != http.StatusBadRequest && res.Code != http.StatusServiceUnavailable {
			t.Fatalf("bad /nfs/enable-upload: expected error, got %d", res.Code)
		}
	}

	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/nfs/enable-upload", strings.NewReader(validMountBody)))
	if res.Code != http.StatusNoContent {
		t.Fatalf("valid /nfs/enable-upload: expected 204, got %d: %s", res.Code, res.Body.String())
	}

	// POST /nfs/check-upload
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/nfs/check-upload", strings.NewReader(`{"key":"0123456789abcdef0123456789abcdef"}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("valid /nfs/check-upload: expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"writable":true`) {
		t.Fatalf("expected writable:true in %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/nfs/check-upload", strings.NewReader(`{"key":"bad"}`)))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("invalid check-upload key: expected 400, got %d", res.Code)
	}

	// POST /nfs/remove
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/nfs/remove", strings.NewReader(`{"key":"bad"}`)))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("invalid remove key: expected 400, got %d", res.Code)
	}

	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/nfs/remove", strings.NewReader(`{"key":"0123456789abcdef0123456789abcdef"}`)))
	if res.Code != http.StatusNoContent {
		t.Fatalf("valid /nfs/remove: expected 204, got %d: %s", res.Code, res.Body.String())
	}
}
