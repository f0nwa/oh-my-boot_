package agent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsISOAgentRejectsUnsafeRequestBeforeSystemd(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "key7")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	systemctl := &fakeWindowsISOSystemctl{mountRoot: t.TempDir(), assetRoot: t.TempDir()}
	manager := &WindowsISOManager{SourceRoot: base, ConfigRoot: t.TempDir(), MountRoot: systemctl.mountRoot, Systemctl: systemctl}
	h := HandlerWithManagers(nil, nil, manager)
	for _, body := range []string{
		`{"id":17,"storage_root":"/etc","relative_path":"passwd","size":100}`,
		`{"id":17,"storage_root":"` + storage + `","relative_path":"../outside.iso","size":100}`,
		`{"id":17,"storage_root":"` + storage + `","relative_path":"win10.iso","size":100,"unknown":true}`,
	} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/windows-iso/prepare", bytes.NewBufferString(body)))
		if res.Code < 400 || len(systemctl.calls) != 0 {
			t.Fatalf("unsafe request reached systemd: %d %+v", res.Code, systemctl.calls)
		}
	}
}

func TestWindowsISOAgentPreparesRegisteredSource(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "key7")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	iso := filepath.Join(storage, "win10.iso")
	if err := os.WriteFile(iso, []byte("ISO"), 0444); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(iso)
	if err != nil {
		t.Fatal(err)
	}
	mountRoot := t.TempDir()
	assetRoot := t.TempDir()
	systemctl := &fakeWindowsISOSystemctl{mountRoot: mountRoot, assetRoot: assetRoot}
	manager := &WindowsISOManager{SourceRoot: base, ConfigRoot: t.TempDir(), MountRoot: mountRoot, AssetRoot: assetRoot, Systemctl: systemctl, Mounter: &fakeWindowsISOMounter{mounted: true}, WIMCommands: fakeWIMInfoRunner{}}
	req := WindowsISORequest{ID: 17, StorageRoot: storage, RelativePath: "win10.iso", Size: info.Size(), Modified: info.ModTime()}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	HandlerWithManagers(nil, nil, manager).ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/windows-iso/prepare", bytes.NewReader(body)))
	if res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"install_file":"install.wim"`)) || !bytes.Contains(res.Body.Bytes(), []byte(`"known":true`)) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	check := httptest.NewRecorder()
	HandlerWithManagers(nil, nil, manager).ServeHTTP(check, httptest.NewRequest(http.MethodPost, "/windows-iso/check", bytes.NewReader(body)))
	if check.Code != http.StatusOK || !bytes.Contains(check.Body.Bytes(), []byte(`"known":true`)) {
		t.Fatalf("prepared profile status=%d: %s", check.Code, check.Body.String())
	}
}
