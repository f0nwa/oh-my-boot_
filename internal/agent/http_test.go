package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerConfigShowsOnlySuccessfulApply(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{}
	a, err := NewApplier(filepath.Join(dir, "dnsmasq.conf"), filepath.Join(dir, "state.json"), runner)
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
	a, err := NewApplier(filepath.Join(dir, "dnsmasq.conf"), filepath.Join(dir, "state.json"), runner)
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
