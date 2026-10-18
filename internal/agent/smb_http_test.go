package agent

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentSMBEndpointAcceptsMountAndRemoveWithoutEchoingPassword(t *testing.T) {
	base := t.TempDir()
	applier, err := NewApplier(filepath.Join(base, "dnsmasq.conf"), filepath.Join(base, "network.json"), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeSMBSystemctl{}
	manager := testSMBManager(t, runner)
	handler := HandlerWithSMB(applier, manager)
	request := `{"key":"0123456789abcdef0123456789abcdef","server":"192.168.10.10","share":"Downloads","subpath":"iso","username":"reader","password":"secret"}`
	mount := httptest.NewRecorder()
	handler.ServeHTTP(mount, httptest.NewRequest("POST", "/smb/mount", strings.NewReader(request)))
	if mount.Code != http.StatusNoContent || strings.Contains(mount.Body.String(), "secret") {
		t.Fatalf("mount status=%d body=%s", mount.Code, mount.Body.String())
	}
	remove := httptest.NewRecorder()
	handler.ServeHTTP(remove, httptest.NewRequest("POST", "/smb/remove", strings.NewReader(`{"key":"0123456789abcdef0123456789abcdef"}`)))
	if remove.Code != http.StatusNoContent {
		t.Fatalf("remove status=%d body=%s", remove.Code, remove.Body.String())
	}
}

func TestAgentSMBEndpointRejectsExtraFields(t *testing.T) {
	base := t.TempDir()
	applier, err := NewApplier(filepath.Join(base, "dnsmasq.conf"), filepath.Join(base, "network.json"), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithSMB(applier, testSMBManager(t, &fakeSMBSystemctl{}))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("POST", "/smb/mount", strings.NewReader(`{"key":"0123456789abcdef0123456789abcdef","unexpected":"x"}`)))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", res.Code)
	}
}

func TestAgentSMBEnableUploadRequiresExistingStorageAndAcceptsMatchingSource(t *testing.T) {
	base := t.TempDir()
	applier, err := NewApplier(filepath.Join(base, "dnsmasq.conf"), filepath.Join(base, "network.json"), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	manager := testSMBManager(t, &fakeSMBSystemctl{})
	handler := HandlerWithSMB(applier, manager)
	request := `{"key":"0123456789abcdef0123456789abcdef","server":"192.168.10.10","share":"Downloads","subpath":"iso"}`
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest("POST", "/smb/enable-upload", strings.NewReader(request)))
	if missing.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing storage status=%d", missing.Code)
	}
	if err := manager.Mount(httptest.NewRequest("GET", "/", nil).Context(), validSMBRequest()); err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("POST", "/smb/enable-upload", strings.NewReader(request)))
	if res.Code != http.StatusNoContent {
		t.Fatalf("upload mount status=%d body=%s", res.Code, res.Body.String())
	}
}
