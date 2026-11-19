package boot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaintenanceRewritesMenuAndBlocksAssetsWithoutMutatingSnapshot(t *testing.T) {
	dir := t.TempDir()
	winDir := filepath.Join(dir, "windows-profiles", "17")
	if err := os.MkdirAll(winDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winDir, "boot.wim"), []byte("wim-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	rawMenu := `#!ipxe
:menu_root
menu oh my boot_
item local Boot from local disk
item install_101 Windows 11 Pro Office
item install_102 Windows 10 LTSC
choose target || goto local
goto ${target}
:install_101
kernel http://10.30.0.10:8080/assets/windows/wimboot
initrd http://10.30.0.10:8080/assets/windows-profiles/17/boot.wim boot.wim
boot || goto install_101_failed
:install_101_failed
echo failed
goto local
:install_102
kernel http://10.30.0.10:8080/assets/windows/wimboot
boot
:local
exit
`
	p, err := NewPublisher(filepath.Join(dir, "menu.ipxe"), []byte(rawMenu))
	if err != nil {
		t.Fatal(err)
	}
	maintPath := filepath.Join(dir, "maintenance.json")
	m := NewMaintenanceManager(maintPath)
	p.SetMaintenance(m)

	h, err := NewDynamicHandlerWithTransfers(dir, p, nil, "", NewTransfers())
	if err != nil {
		t.Fatal(err)
	}
	ctrl := ControlHandlerWithTransfers(p, nil, nil, nil)

	// Enable maintenance via Control API
	putRec := httptest.NewRecorder()
	ctrl.ServeHTTP(putRec, httptest.NewRequest(http.MethodPut, "/maintenance", strings.NewReader(`{"installation_ids":[101],"windows_profile_ids":[17]}`)))
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT /maintenance = %d %s", putRec.Code, putRec.Body.String())
	}

	// GET /boot/menu.ipxe shows [MAINTENANCE] and pause screen for install_101, while install_102 stays intact
	menuRec := httptest.NewRecorder()
	h.ServeHTTP(menuRec, httptest.NewRequest(http.MethodGet, "/boot/menu.ipxe", nil))
	body := menuRec.Body.String()
	if !strings.Contains(body, "item install_101 Windows 11 Pro Office [MAINTENANCE]") {
		t.Fatalf("missing [MAINTENANCE] label in menu:\n%s", body)
	}
	if !strings.Contains(body, "[MAINTENANCE] This installation is temporarily paused for maintenance.") || !strings.Contains(body, "goto menu_root") {
		t.Fatalf("missing pause screen in menu:\n%s", body)
	}
	if strings.Contains(body, "initrd http://10.30.0.10:8080/assets/windows-profiles/17/boot.wim") {
		t.Fatalf("install_101 boot commands were not replaced:\n%s", body)
	}
	if !strings.Contains(body, "item install_102 Windows 10 LTSC\n") || !strings.Contains(body, ":install_102\nkernel") {
		t.Fatalf("unrelated item install_102 was damaged:\n%s", body)
	}

	// Stored publisher snapshot remains untouched
	stored, _ := p.Current()
	if string(stored) != rawMenu {
		t.Fatal("publisher snapshot was mutated by maintenance mode")
	}

	// New HTTP asset download for blocked profile returns 503
	assetRec := httptest.NewRecorder()
	h.ServeHTTP(assetRec, httptest.NewRequest(http.MethodGet, "/assets/windows-profiles/17/boot.wim", nil))
	if assetRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("blocked asset returned %d, want 503", assetRec.Code)
	}

	// State persists across restart
	reloaded := NewMaintenanceManager(maintPath)
	var got MaintenanceState
	getRec := httptest.NewRecorder()
	ctrl.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/maintenance", nil))
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil || len(got.InstallationIDs) != 1 || len(reloaded.Current().InstallationIDs) != 1 {
		t.Fatalf("persistence failed: got=%+v reloaded=%+v", got, reloaded.Current())
	}

	// Clearing maintenance restores menu and asset download immediately
	clearRec := httptest.NewRecorder()
	ctrl.ServeHTTP(clearRec, httptest.NewRequest(http.MethodPut, "/maintenance", strings.NewReader(`{}`)))
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear PUT /maintenance = %d", clearRec.Code)
	}
	menuAfter := httptest.NewRecorder()
	h.ServeHTTP(menuAfter, httptest.NewRequest(http.MethodGet, "/boot/menu.ipxe", nil))
	if menuAfter.Body.String() != rawMenu {
		t.Fatalf("menu not restored after clearing maintenance:\n%s", menuAfter.Body.String())
	}
	assetAfter := httptest.NewRecorder()
	h.ServeHTTP(assetAfter, httptest.NewRequest(http.MethodGet, "/assets/windows-profiles/17/boot.wim", nil))
	if assetAfter.Code != http.StatusOK {
		t.Fatalf("asset not restored after clearing maintenance: %d", assetAfter.Code)
	}
}
