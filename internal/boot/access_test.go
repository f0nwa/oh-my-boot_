package boot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAccessPolicyManagerPersistenceAndMatching(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.json")

	// Env fallback when access.json is absent.
	m := NewAccessPolicyManager(path, "192.168.10.0/24, 10.20.0.0/16")
	if !m.Allows("192.168.10.55:1234") {
		t.Fatal("expected 192.168.10.55 to be allowed")
	}
	if !m.Allows("[::ffff:10.20.5.9]:4321") {
		t.Fatal("expected IPv4-mapped IPv6 in 10.20.0.0/16 to be allowed")
	}
	if m.Allows("10.30.0.19:1234") {
		t.Fatal("expected 10.30.0.19 to be denied when env overrides default")
	}

	// Dynamic update persists to disk.
	if err := m.Set(ClientAccessPolicy{Mode: AccessModeSubnets, Subnets: []string{"172.16.0.0/24"}}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !m.Allows("172.16.0.42:9000") || m.Allows("192.168.10.55:1234") {
		t.Fatal("updated policy not applied")
	}

	// Reloading prefers persisted access.json over env.
	reloaded := NewAccessPolicyManager(path, "192.168.10.0/24")
	if !reloaded.Allows("172.16.0.42:9000") || reloaded.Allows("192.168.10.55:1234") {
		t.Fatal("persisted access.json did not override env")
	}

	// Mode "any" allows any valid IP.
	if err := reloaded.Set(ClientAccessPolicy{Mode: AccessModeAny}); err != nil {
		t.Fatalf("Set any: %v", err)
	}
	if !reloaded.Allows("198.51.100.77:5555") || reloaded.Allows("not-an-addr") {
		t.Fatal("any mode matching failed")
	}
}

func TestHTTPHandlerUsesDynamicAccessPolicyAndControlAPI(t *testing.T) {
	dir := t.TempDir()
	winDir := filepath.Join(dir, "windows-profiles", "1")
	if err := os.MkdirAll(winDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winDir, "omb-setup.cmd"), []byte("@echo off\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pub, err := NewPublisher(filepath.Join(dir, "menu.ipxe"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	access := NewAccessPolicyManager(filepath.Join(dir, "access.json"), "")
	pub.SetAccess(access)

	var events []Event
	handler, err := NewDynamicHandler(dir, pub, func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	ctrl := ControlHandler(pub, nil, nil)

	// Initially corporate IP 192.168.10.50 is rejected with 404 and logged.
	req := httptest.NewRequest(http.MethodGet, "/assets/windows-profiles/1/omb-setup.cmd", nil)
	req.RemoteAddr = "192.168.10.50:50000"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("initial corporate client status = %d, want 404", res.Code)
	}
	if len(events) != 1 || events[0].Status != http.StatusNotFound {
		t.Fatalf("expected 404 event recorded, got %+v", events)
	}

	// Update via Control API PUT /access.
	body, _ := json.Marshal(ClientAccessPolicy{Mode: AccessModeSubnets, Subnets: []string{"192.168.10.0/24", "10.20.0.0/16"}})
	putReq := httptest.NewRequest(http.MethodPut, "/access", bytes.NewReader(body))
	putRes := httptest.NewRecorder()
	ctrl.ServeHTTP(putRes, putReq)
	if putRes.Code != http.StatusOK {
		t.Fatalf("PUT /access status = %d, body = %s", putRes.Code, putRes.Body.String())
	}

	// Now corporate IP and routed VLAN IP both succeed.
	for _, remote := range []string{"192.168.10.50:50001", "10.20.5.8:50002"} {
		r := httptest.NewRequest(http.MethodGet, "/assets/windows-profiles/1/omb-setup.cmd", nil)
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("client %s status = %d, want 200", remote, w.Code)
		}
	}

	// GET /access returns active policy.
	getRes := httptest.NewRecorder()
	ctrl.ServeHTTP(getRes, httptest.NewRequest(http.MethodGet, "/access", nil))
	if getRes.Code != http.StatusOK {
		t.Fatalf("GET /access status = %d", getRes.Code)
	}
	var got ClientAccessPolicy
	if err := json.Unmarshal(getRes.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET /access: %v", err)
	}
	if got.Mode != AccessModeSubnets || len(got.Subnets) != 2 {
		t.Fatalf("GET /access = %+v", got)
	}
}
