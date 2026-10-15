package agent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

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
