package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentSMBEndpointAcceptsMountAndRemoveWithoutEchoingPassword(t *testing.T) {
	base := t.TempDir()
	applier, err := NewApplier(filepath.Join(base, "dnsmasq.conf"), filepath.Join(base, "network.json"), filepath.Join(base, "dhcp", "hosts"), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeSMBSystemctl{}
	manager := testSMBManager(t, &fakeSMBSystemctl{})
	manager.Systemctl = runner
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
	applier, err := NewApplier(filepath.Join(base, "dnsmasq.conf"), filepath.Join(base, "network.json"), filepath.Join(base, "dhcp", "hosts"), &fakeRunner{})
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
	applier, err := NewApplier(filepath.Join(base, "dnsmasq.conf"), filepath.Join(base, "network.json"), filepath.Join(base, "dhcp", "hosts"), &fakeRunner{})
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

func TestAgentSMBAccountsListsUsernamesWithoutPasswords(t *testing.T) {
	base := t.TempDir()
	applier, err := NewApplier(filepath.Join(base, "dnsmasq.conf"), filepath.Join(base, "network.json"), filepath.Join(base, "dhcp", "hosts"), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	manager := testSMBManager(t, &fakeSMBSystemctl{})
	handler := HandlerWithSMB(applier, manager)

	req1 := `{"key":"0123456789abcdef0123456789abcdef","server":"192.168.10.10","share":"ISO","username":"omb-pxe","password":"top-secret-password-1"}`
	req2 := `{"key":"fedcba9876543210fedcba9876543210","server":"192.168.10.11","share":"Archive","username":"svc-omb","password":"top-secret-password-2","domain":"CORP"}`
	for _, body := range []string{req1, req2} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("POST", "/smb/mount", strings.NewReader(body)))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("mount failed: %d %s", rec.Code, rec.Body.String())
		}
	}

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("GET", "/smb/accounts", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("GET /smb/accounts status=%d body=%s", res.Code, res.Body.String())
	}
	raw := res.Body.String()
	if strings.Contains(raw, "top-secret-password") {
		t.Fatalf("GET /smb/accounts leaked password: %s", raw)
	}
	var accounts map[string]SMBAccountInfo
	if err := json.Unmarshal(res.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("decode accounts: %v", err)
	}
	if got := accounts["0123456789abcdef0123456789abcdef"]; got.Username != "omb-pxe" || got.Domain != "" {
		t.Fatalf("unexpected account 1: %+v", got)
	}
	if got := accounts["fedcba9876543210fedcba9876543210"]; got.Username != "svc-omb" || got.Domain != "CORP" {
		t.Fatalf("unexpected account 2: %+v", got)
	}
}

type trackingSystemctl struct {
	calls  []string
	active map[string]bool
}

func (f *trackingSystemctl) Run(_ context.Context, args ...string) error {
	cmd := strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	if len(args) >= 3 && args[0] == "is-active" && args[1] == "--quiet" {
		if f.active[args[2]] {
			return nil
		}
		return os.ErrNotExist
	}
	if len(args) >= 2 && args[0] == "stop" {
		f.active[args[1]] = false
	}
	if len(args) >= 2 && args[0] == "start" {
		f.active[args[1]] = true
	}
	if len(args) >= 3 && args[0] == "enable" && args[1] == "--now" {
		f.active[args[2]] = true
	}
	return nil
}

func TestAgentSMBMountReconnectStopsAndRecoversWindowsProfiles(t *testing.T) {
	base := t.TempDir()
	applier, err := NewApplier(filepath.Join(base, "dnsmasq.conf"), filepath.Join(base, "network.json"), filepath.Join(base, "dhcp", "hosts"), &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	profileUnit := "omb-winiso@7.service"
	runner := &trackingSystemctl{active: map[string]bool{profileUnit: true}}
	manager := testSMBManager(t, &fakeSMBSystemctl{})
	manager.Systemctl = runner
	key := "0123456789abcdef0123456789abcdef"
	shareTarget := filepath.Join(manager.MountRoot, key)
	profileCfgDir := filepath.Join(base, "profiles")
	if err := os.MkdirAll(profileCfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileCfgDir, "7.json"), []byte(`{"profile_id":7,"storage_root":"`+shareTarget+`","iso_path":"win11.iso"}`), 0644); err != nil {
		t.Fatal(err)
	}
	windows := &WindowsISOManager{
		ConfigRoot: profileCfgDir,
		MountRoot:  filepath.Join(base, "mnt"),
		Systemctl:  runner,
	}
	handler := HandlerWithManagers(applier, manager, windows)

	// Initial mount under old user.
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, httptest.NewRequest("POST", "/smb/mount", strings.NewReader(`{"key":"`+key+`","server":"192.168.10.10","share":"ISO","username":"old-user","password":"old-password"}`)))
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("initial mount status=%d body=%s", rec1.Code, rec1.Body.String())
	}
	if err := os.WriteFile(filepath.Join(shareTarget, "win11.iso"), []byte("iso"), 0644); err != nil {
		t.Fatal(err)
	}
	runner.calls = nil

	// Reconnect under new user: must stop the Windows profile unit BEFORE stopping the SMB mount, then recover it AFTER.
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, httptest.NewRequest("POST", "/smb/mount", strings.NewReader(`{"key":"`+key+`","server":"192.168.10.10","share":"ISO","username":"new-user","password":"new-password"}`)))
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("reconnect status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	smbUnit := smbUnitName(shareTarget)
	stopProfileIdx, stopSMBIdx, startProfileIdx := -1, -1, -1
	for i, c := range runner.calls {
		if c == "stop "+profileUnit && stopProfileIdx == -1 {
			stopProfileIdx = i
		}
		if c == "stop "+smbUnit && stopSMBIdx == -1 {
			stopSMBIdx = i
		}
		if c == "restart "+profileUnit || c == "start "+profileUnit {
			startProfileIdx = i
		}
	}
	if stopProfileIdx == -1 || stopSMBIdx == -1 || startProfileIdx == -1 {
		t.Fatalf("expected stop profile, stop smb, and start profile calls; got %v", runner.calls)
	}
	if !(stopProfileIdx < stopSMBIdx && stopSMBIdx < startProfileIdx) {
		t.Fatalf("wrong order: stopProfile=%d stopSMB=%d startProfile=%d calls=%v", stopProfileIdx, stopSMBIdx, startProfileIdx, runner.calls)
	}
	if got := manager.ListAccounts()[key]; got.Username != "new-user" {
		t.Fatalf("expected updated username new-user, got %+v", got)
	}
}
