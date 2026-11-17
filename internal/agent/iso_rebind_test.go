package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsISOManagerRebind(t *testing.T) {
	base := t.TempDir()
	src1 := filepath.Join(base, "key1")
	src2 := filepath.Join(base, "key2")
	if err := os.MkdirAll(src1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(src2, 0755); err != nil {
		t.Fatal(err)
	}

	configRoot := t.TempDir()
	mountRoot := t.TempDir()
	assetRoot := t.TempDir()

	testISO := "win11.iso"
	content := []byte("ISO1")
	file1 := filepath.Join(src1, testISO)
	if err := os.WriteFile(file1, content, 0644); err != nil {
		t.Fatal(err)
	}
	info1, _ := os.Stat(file1)

	file2 := filepath.Join(src2, testISO)
	if err := os.WriteFile(file2, content, 0644); err != nil {
		t.Fatal(err)
	}
	info2, _ := os.Stat(file2)

	systemctl := &fakeWindowsISOSystemctl{mountRoot: mountRoot, assetRoot: assetRoot}
	manager := &WindowsISOManager{
		SourceRoots: []string{base},
		ConfigRoot:  configRoot,
		MountRoot:   mountRoot,
		AssetRoot:   assetRoot,
		Systemctl:   systemctl,
		Mounter:     &fakeWindowsISOMounter{mounted: true},
		WIMCommands: fakeWIMInfoRunner{},
	}

	req1 := WindowsISORequest{
		ID:           17,
		StorageRoot:  src1,
		RelativePath: testISO,
		Size:         info1.Size(),
		Modified:     info1.ModTime().Truncate(time.Microsecond),
	}

	// Prepare initial profile
	res, err := manager.Prepare(context.Background(), req1)
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if res.InstallFile != "install.wim" {
		t.Fatalf("unexpected install file: %s", res.InstallFile)
	}

	// Rebind to src2
	req2 := WindowsISORequest{
		ID:           17,
		StorageRoot:  src2,
		RelativePath: testISO,
		Size:         info2.Size(),
		Modified:     info2.ModTime().Truncate(time.Microsecond),
	}
	res2, err := manager.Rebind(context.Background(), req2)
	if err != nil {
		t.Fatalf("Rebind failed: %v", err)
	}
	if res2.InstallFile != "install.wim" {
		t.Fatalf("unexpected install file after rebind: %s", res2.InstallFile)
	}

	// Verify Check passes with req2
	checkRes, err := manager.Check(context.Background(), req2)
	if err != nil {
		t.Fatalf("Check failed after rebind: %v", err)
	}
	if checkRes.InstallFile != "install.wim" {
		t.Fatalf("unexpected install file on check: %s", checkRes.InstallFile)
	}
}

func TestUbuntuISOManagerRebind(t *testing.T) {
	base := t.TempDir()
	src1 := filepath.Join(base, "key1")
	src2 := filepath.Join(base, "key2")
	if err := os.MkdirAll(src1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(src2, 0755); err != nil {
		t.Fatal(err)
	}

	assetRoot := t.TempDir()
	registryRoot := t.TempDir()

	testISO := "ubuntu.iso"
	content := []byte("UBUNTU-DATA")
	file1 := filepath.Join(src1, testISO)
	if err := os.WriteFile(file1, content, 0644); err != nil {
		t.Fatal(err)
	}
	info1, _ := os.Stat(file1)

	file2 := filepath.Join(src2, testISO)
	if err := os.WriteFile(file2, content, 0644); err != nil {
		t.Fatal(err)
	}
	info2, _ := os.Stat(file2)

	// sha256 of UBUNTU-DATA is computed:
	sha := "4d0d2902329ed6a07b08fd3db42082be1b7a52016f24cb31c10ad6089e48a453"
	req1 := UbuntuISORequest{
		WindowsISORequest: WindowsISORequest{
			ID:           101,
			StorageRoot:  src1,
			RelativePath: testISO,
			Size:         info1.Size(),
			Modified:     info1.ModTime().Truncate(time.Microsecond),
		},
		SHA256: sha,
	}

	manager := &UbuntuISOManager{
		SourceRoots:  []string{base},
		AssetRoot:    assetRoot,
		RegistryRoot: registryRoot,
		ScriptPath:   "/bin/true",
		Runner:       &fakeDriverRunner{},
	}

	if err := manager.Rebind(context.Background(), req1); err != nil {
		t.Fatalf("initial Rebind failed: %v", err)
	}

	// Verify registry file has src1
	data, err := os.ReadFile(filepath.Join(registryRoot, "101.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), src1) {
		t.Fatalf("registry should contain src1, got %s", string(data))
	}

	// Rebind to src2
	req2 := req1
	req2.StorageRoot = src2
	req2.Modified = info2.ModTime().Truncate(time.Microsecond)
	if err := manager.Rebind(context.Background(), req2); err != nil {
		t.Fatalf("second Rebind failed: %v", err)
	}

	// Verify registry file now has src2
	data2, err := os.ReadFile(filepath.Join(registryRoot, "101.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data2), src2) {
		t.Fatalf("registry should contain src2, got %s", string(data2))
	}
}

func TestAgentRebindHTTPHandlers(t *testing.T) {
	base := t.TempDir()
	src1 := filepath.Join(base, "key1")
	if err := os.MkdirAll(src1, 0755); err != nil {
		t.Fatal(err)
	}

	configRoot := t.TempDir()
	mountRoot := t.TempDir()
	assetRoot := t.TempDir()
	registryRoot := t.TempDir()

	testISO := "win11.iso"
	content := []byte("ISO-WIN")
	file1 := filepath.Join(src1, testISO)
	if err := os.WriteFile(file1, content, 0644); err != nil {
		t.Fatal(err)
	}
	info1, _ := os.Stat(file1)

	systemctl := &fakeWindowsISOSystemctl{mountRoot: mountRoot, assetRoot: assetRoot}
	winManager := &WindowsISOManager{
		SourceRoots: []string{base},
		ConfigRoot:  configRoot,
		MountRoot:   mountRoot,
		AssetRoot:   assetRoot,
		Systemctl:   systemctl,
		Mounter:     &fakeWindowsISOMounter{mounted: true},
		WIMCommands: fakeWIMInfoRunner{},
	}
	ubuntuManager := &UbuntuISOManager{
		SourceRoots:  []string{base},
		AssetRoot:    assetRoot,
		RegistryRoot: registryRoot,
		ScriptPath:   "/bin/true",
		Runner:       &fakeDriverRunner{},
	}

	handler := HandlerWithNFS(HandlerWithUbuntuISO(HandlerWithDriverManager(&Applier{}, nil, winManager, nil), ubuntuManager), nil)

	// 1. POST /windows-iso/rebind
	winReq := WindowsISORequest{
		ID:           17,
		StorageRoot:  src1,
		RelativePath: testISO,
		Size:         info1.Size(),
		Modified:     info1.ModTime().Truncate(time.Microsecond),
	}
	winJSON, _ := json.Marshal(winReq)
	req1, _ := http.NewRequest(http.MethodPost, "/windows-iso/rebind", bytes.NewReader(winJSON))
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("windows-iso/rebind expected 200, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// 2. POST /ubuntu-iso/rebind
	sha := "c77de8a9a4b3341b52a5c3d2e95a1a1f0efda3f2a7db5c51722d56a312bc85bc" // dummy 64 hex
	// let's compute real sha of content:
	hash := sha256.Sum256(content)
	sha = hex.EncodeToString(hash[:])
	ubuntuReq := UbuntuISORequest{
		WindowsISORequest: WindowsISORequest{
			ID:           101,
			StorageRoot:  src1,
			RelativePath: testISO,
			Size:         info1.Size(),
			Modified:     info1.ModTime().Truncate(time.Microsecond),
		},
		SHA256: sha,
	}
	ubuntuJSON, _ := json.Marshal(ubuntuReq)
	req2, _ := http.NewRequest(http.MethodPost, "/ubuntu-iso/rebind", bytes.NewReader(ubuntuJSON))
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("ubuntu-iso/rebind expected 204, got %d: %s", rec2.Code, rec2.Body.String())
	}
}
