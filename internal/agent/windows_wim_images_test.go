package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeWIMImageRunner struct {
	infoOut map[string]string
	dirOut  map[string]string
	dirErr  map[string]error
	verify  error
	calls   []string
}

func (f *fakeWIMImageRunner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if name != "wimlib-imagex" || len(args) < 2 {
		return nil, errors.New("unexpected command: " + call)
	}
	sub := args[0]
	target := args[1]
	switch sub {
	case "info":
		if out, ok := f.infoOut[target]; ok {
			return []byte(out), nil
		}
		if out, ok := f.infoOut["*"]; ok {
			return []byte(out), nil
		}
		return nil, errors.New("no fake info output for " + target)
	case "dir":
		if err, ok := f.dirErr[target]; ok {
			return nil, err
		}
		if err, ok := f.dirErr["*"]; ok {
			return nil, err
		}
		if out, ok := f.dirOut[target]; ok {
			return []byte(out), nil
		}
		if out, ok := f.dirOut["*"]; ok {
			return []byte(out), nil
		}
		return []byte(""), nil
	case "verify":
		return nil, f.verify
	default:
		return nil, errors.New("unexpected wimlib-imagex subcommand: " + sub)
	}
}

type fakeInstallMounter struct {
	mounted      map[string]string // target -> source
	mountCalls   []string
	unmountCalls []string
	failMount    error
}

func newFakeInstallMounter() *fakeInstallMounter {
	return &fakeInstallMounter{mounted: map[string]string{}}
}

func (f *fakeInstallMounter) IsMounted(_ context.Context, target string) (bool, error) {
	_, ok := f.mounted[target]
	return ok, nil
}

func (f *fakeInstallMounter) BindMountRO(_ context.Context, source, target string) error {
	f.mountCalls = append(f.mountCalls, source+" -> "+target)
	if f.failMount != nil {
		return f.failMount
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("bind-mount target file must exist as a regular file")
	}
	f.mounted[target] = source
	return nil
}

func (f *fakeInstallMounter) Unmount(_ context.Context, target string) error {
	f.unmountCalls = append(f.unmountCalls, target)
	delete(f.mounted, target)
	return nil
}

const sampleBootWIMWithSetupInfo = `WIM Information:
----------------
Image Count:    2
Boot Index:     2

Available Images:
-----------------
Index:                  1
Name:                   Microsoft Windows PE (amd64)
Architecture:           x86_64
Flags:                  9
Build:                  26100

Index:                  2
Name:                   Microsoft Windows Setup (amd64)
Architecture:           x86_64
Flags:                  2
Build:                  26100
`

const sampleADKBootWIMInfo = `WIM Information:
----------------
Image Count:    1
Boot Index:     1

Available Images:
-----------------
Index:                  1
Name:                   Microsoft Windows PE (amd64)
Architecture:           x86_64
Flags:                  9
Build:                  26100
`

const sampleInstallWIMInfo = `WIM Information:
----------------
Image Count:    1
Boot Index:     0

Available Images:
-----------------
Index:                  1
Name:                   Windows 11 Pro
Display Name:           Windows 11 Pro
Architecture:           x86_64
Edition ID:             Professional
Build:                  26100
Languages:              en-US
Default Language:       en-US
`

const sampleWinPEZeroBootIndexInfo = `WIM Information:
----------------
Image Count:    1
Boot Index:     0

Available Images:
-----------------
Index:                  1
Name:                   Microsoft Windows PE (amd64)
Architecture:           x86_64
Flags:                  WindowsPE
Build:                  26100
`

func writeSourceWIM(t *testing.T, dir, rel string, content []byte) (string, os.FileInfo) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	return full, info
}

func TestPrepareBootImageDetectsSetupExeAndRejectsNonBootWIM(t *testing.T) {
	base := canonicalTestDir(t)
	smbRoot := filepath.Join(base, "smb")
	storage := filepath.Join(smbRoot, "nas1")
	bootRoot := filepath.Join(base, "tftp", "windows-boot-images")

	bootBytes := []byte("fake boot.wim with setup.exe")
	_, bootInfo := writeSourceWIM(t, storage, "win11/boot.wim", bootBytes)
	wantSum := sha256.Sum256(bootBytes)
	wantSHA := hex.EncodeToString(wantSum[:])

	runner := &fakeWIMImageRunner{
		infoOut: map[string]string{"*": sampleBootWIMWithSetupInfo},
		dirOut:  map[string]string{"*": "/sources\n/sources/setup.exe\n/sources/spwizeng.dll\n"},
	}
	m := &WindowsISOManager{
		SourceRoots:   []string{smbRoot},
		BootImageRoot: bootRoot,
		WIMCommands:   runner,
	}

	// 1. WIM with Boot Index: 2 and /sources/setup.exe -> HasSetupExe = true, copied, SHA-256 computed.
	res, err := m.PrepareBootImage(context.Background(), WindowsBootImageRequest{
		ID:           10,
		StorageRoot:  storage,
		RelativePath: "win11/boot.wim",
		Size:         bootInfo.Size(),
		Modified:     bootInfo.ModTime(),
	})
	if err != nil {
		t.Fatalf("PrepareBootImage with setup.exe failed: %v", err)
	}
	if !res.HasSetupExe {
		t.Fatalf("expected HasSetupExe=true, got false")
	}
	if res.SHA256 != wantSHA {
		t.Fatalf("SHA256 = %q, want %q", res.SHA256, wantSHA)
	}
	if !res.WIMInfo.Known || len(res.WIMInfo.Images) != 2 {
		t.Fatalf("unexpected WIMInfo: %+v", res.WIMInfo)
	}
	copied, err := os.ReadFile(filepath.Join(bootRoot, "10", "boot.wim"))
	if err != nil || !bytes.Equal(copied, bootBytes) {
		t.Fatalf("boot.wim not copied properly: err=%v len=%d", err, len(copied))
	}

	// 2. Pure ADK WIM with Boot Index: 1 and no /sources/setup.exe -> HasSetupExe = false, succeeds.
	adkBytes := []byte("fake adk winpe boot.wim")
	_, adkInfo := writeSourceWIM(t, storage, "adk/winpe.wim", adkBytes)
	m.WIMCommands = &fakeWIMImageRunner{
		infoOut: map[string]string{"*": sampleADKBootWIMInfo},
		dirErr:  map[string]error{"*": errors.New("path /sources not found in image")},
	}
	resADK, err := m.PrepareBootImage(context.Background(), WindowsBootImageRequest{
		ID:           11,
		StorageRoot:  storage,
		RelativePath: "adk/winpe.wim",
		Size:         adkInfo.Size(),
		Modified:     adkInfo.ModTime(),
	})
	if err != nil {
		t.Fatalf("PrepareBootImage for pure ADK failed: %v", err)
	}
	if resADK.HasSetupExe {
		t.Fatalf("expected HasSetupExe=false for ADK WinPE, got true")
	}

	// 3. Regular install.wim with Boot Index: 0 -> rejected (Review Focus #1).
	installBytes := []byte("fake install.wim uploaded to boot tab")
	_, installInfo := writeSourceWIM(t, storage, "wrong/install.wim", installBytes)
	m.WIMCommands = &fakeWIMImageRunner{
		infoOut: map[string]string{"*": sampleInstallWIMInfo},
	}
	_, err = m.PrepareBootImage(context.Background(), WindowsBootImageRequest{
		ID:           12,
		StorageRoot:  storage,
		RelativePath: "wrong/install.wim",
		Size:         installInfo.Size(),
		Modified:     installInfo.ModTime(),
	})
	if err == nil || !errors.Is(err, ErrInvalidWIMType) {
		t.Fatalf("expected ErrInvalidWIMType for Boot Index: 0, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(bootRoot, "12")); !os.IsNotExist(statErr) {
		t.Fatalf("rejected boot image left directory behind: %v", statErr)
	}

	// 4. RemoveBootImage deletes /windows-boot-images/<id>.
	if err := m.RemoveBootImage(context.Background(), 10); err != nil {
		t.Fatalf("RemoveBootImage failed: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(bootRoot, "10")); !os.IsNotExist(statErr) {
		t.Fatalf("boot image 10 was not removed: %v", statErr)
	}
}

func TestPrepareInstallImageMountsReadOnlyAndRejectsPureWinPE(t *testing.T) {
	base := canonicalTestDir(t)
	smbRoot := filepath.Join(base, "smb")
	storage := filepath.Join(smbRoot, "nas1")
	mountRoot := filepath.Join(base, "mnt", "profiles")
	configRoot := filepath.Join(base, "etc", "winiso")

	wimSrc, wimInfo := writeSourceWIM(t, storage, "win11/install.wim", []byte("fake install.wim"))
	esdSrc, esdInfo := writeSourceWIM(t, storage, "win11/custom.esd", []byte("fake install.esd"))
	_, bootInfo := writeSourceWIM(t, storage, "win11/boot.wim", []byte("fake boot.wim"))

	mounter := newFakeInstallMounter()
	m := &WindowsISOManager{
		SourceRoots:    []string{smbRoot},
		MountRoot:      mountRoot,
		ConfigRoot:     configRoot,
		InstallMounter: mounter,
		WIMCommands: &fakeWIMImageRunner{
			infoOut: map[string]string{
				wimSrc: sampleInstallWIMInfo,
				esdSrc: sampleInstallWIMInfo,
			},
		},
	}

	// 1. Valid install.wim -> bind-mounted ro under install-5/sources/install.wim.
	res, err := m.PrepareInstallImage(context.Background(), WindowsInstallImageRequest{
		ID:           5,
		StorageRoot:  storage,
		RelativePath: "win11/install.wim",
		Size:         wimInfo.Size(),
		Modified:     wimInfo.ModTime(),
	})
	if err != nil {
		t.Fatalf("PrepareInstallImage (.wim) failed: %v", err)
	}
	if res.InstallFile != "install.wim" || !res.WIMInfo.Known || len(res.WIMInfo.Images) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	wantTarget := filepath.Join(mountRoot, "install-5", "sources", "install.wim")
	if mounter.mounted[wantTarget] != wimSrc {
		t.Fatalf("expected bind mount %s -> %s, got %+v", wimSrc, wantTarget, mounter.mounted)
	}

	// 2. Valid .esd -> bind-mounted as install.esd.
	resESD, err := m.PrepareInstallImage(context.Background(), WindowsInstallImageRequest{
		ID:           6,
		StorageRoot:  storage,
		RelativePath: "win11/custom.esd",
		Size:         esdInfo.Size(),
		Modified:     esdInfo.ModTime(),
	})
	if err != nil {
		t.Fatalf("PrepareInstallImage (.esd) failed: %v", err)
	}
	if resESD.InstallFile != "install.esd" {
		t.Fatalf("InstallFile = %q, want install.esd", resESD.InstallFile)
	}
	wantESDTarget := filepath.Join(mountRoot, "install-6", "sources", "install.esd")
	if mounter.mounted[wantESDTarget] != esdSrc {
		t.Fatalf("expected bind mount %s -> %s, got %+v", esdSrc, wantESDTarget, mounter.mounted)
	}

	// 3. Pure boot.wim (Boot Index > 0) -> rejected (Review Focus #1).
	m.WIMCommands = &fakeWIMImageRunner{
		infoOut: map[string]string{"*": sampleBootWIMWithSetupInfo},
	}
	_, err = m.PrepareInstallImage(context.Background(), WindowsInstallImageRequest{
		ID:           7,
		StorageRoot:  storage,
		RelativePath: "win11/boot.wim",
		Size:         bootInfo.Size(),
		Modified:     bootInfo.ModTime(),
	})
	if err == nil || !errors.Is(err, ErrInvalidWIMType) {
		t.Fatalf("expected ErrInvalidWIMType for boot.wim with Boot Index > 0, got %v", err)
	}

	// 4. WinPE WIM with Boot Index: 0 and Flags: WindowsPE -> rejected (Review Focus #1).
	m.WIMCommands = &fakeWIMImageRunner{
		infoOut: map[string]string{"*": sampleWinPEZeroBootIndexInfo},
	}
	_, err = m.PrepareInstallImage(context.Background(), WindowsInstallImageRequest{
		ID:           8,
		StorageRoot:  storage,
		RelativePath: "win11/boot.wim",
		Size:         bootInfo.Size(),
		Modified:     bootInfo.ModTime(),
	})
	if err == nil || !errors.Is(err, ErrInvalidWIMType) {
		t.Fatalf("expected ErrInvalidWIMType for WinPE Flags, got %v", err)
	}

	// 5. RemoveInstallImage unmounts and removes directory + config.
	if err := m.RemoveInstallImage(context.Background(), 5); err != nil {
		t.Fatalf("RemoveInstallImage failed: %v", err)
	}
	if _, stillMounted := mounter.mounted[wantTarget]; stillMounted {
		t.Fatalf("install image 5 still mounted after removal")
	}
	if _, statErr := os.Stat(filepath.Join(mountRoot, "install-5")); !os.IsNotExist(statErr) {
		t.Fatalf("install-5 directory was not removed: %v", statErr)
	}
}

func TestStopAndRecoverInstallImagesOnSMBReconnect(t *testing.T) {
	base := canonicalTestDir(t)
	smbRoot := filepath.Join(base, "smb")
	storageA := filepath.Join(smbRoot, "nas-a")
	storageB := filepath.Join(smbRoot, "nas-b")
	mountRoot := filepath.Join(base, "mnt", "profiles")
	configRoot := filepath.Join(base, "etc", "winiso")

	srcA, infoA := writeSourceWIM(t, storageA, "win11/install.wim", []byte("install A"))
	srcB, infoB := writeSourceWIM(t, storageB, "win10/install.wim", []byte("install B"))

	mounter := newFakeInstallMounter()
	ctl := &recoverySystemctl{active: map[string]bool{}}
	m := &WindowsISOManager{
		SourceRoots:    []string{smbRoot},
		MountRoot:      mountRoot,
		ConfigRoot:     configRoot,
		Systemctl:      ctl,
		InstallMounter: mounter,
		WIMCommands:    &fakeWIMImageRunner{infoOut: map[string]string{"*": sampleInstallWIMInfo}},
	}

	if _, err := m.PrepareInstallImage(context.Background(), WindowsInstallImageRequest{
		ID:           21,
		StorageRoot:  storageA,
		RelativePath: "win11/install.wim",
		Size:         infoA.Size(),
		Modified:     infoA.ModTime(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PrepareInstallImage(context.Background(), WindowsInstallImageRequest{
		ID:           22,
		StorageRoot:  storageB,
		RelativePath: "win10/install.wim",
		Size:         infoB.Size(),
		Modified:     infoB.ModTime(),
	}); err != nil {
		t.Fatal(err)
	}

	targetA := filepath.Join(mountRoot, "install-21", "sources", "install.wim")
	targetB := filepath.Join(mountRoot, "install-22", "sources", "install.wim")

	// StopProfilesOn storageA must unmount install-21 but leave install-22 mounted.
	stopped, err := m.StopProfilesOn(context.Background(), []string{storageA})
	if err != nil {
		t.Fatalf("StopProfilesOn failed: %v", err)
	}
	if len(stopped) == 0 {
		t.Fatalf("expected stop action for install-21, got none")
	}
	if _, ok := mounter.mounted[targetA]; ok {
		t.Fatalf("install-21 was not unmounted on StopProfilesOn")
	}
	if mounter.mounted[targetB] != srcB {
		t.Fatalf("install-22 on unrelated storage was unmounted")
	}

	// RecoverProfiles must restore install-21 bind-mount.
	recovered, err := m.RecoverProfiles(context.Background())
	if err != nil {
		t.Fatalf("RecoverProfiles failed: %v", err)
	}
	if len(recovered) == 0 {
		t.Fatalf("expected recovery action for install-21, got none")
	}
	if mounter.mounted[targetA] != srcA {
		t.Fatalf("install-21 was not remounted by RecoverProfiles: %+v", mounter.mounted)
	}
}

func TestWindowsWIMImagesHTTPEndpoints(t *testing.T) {
	base := canonicalTestDir(t)
	smbRoot := filepath.Join(base, "smb")
	storage := filepath.Join(smbRoot, "nas1")
	mountRoot := filepath.Join(base, "mnt", "profiles")
	configRoot := filepath.Join(base, "etc", "winiso")
	bootRoot := filepath.Join(base, "tftp", "windows-boot-images")

	_, bootInfo := writeSourceWIM(t, storage, "boot.wim", []byte("boot wim"))
	_, instInfo := writeSourceWIM(t, storage, "install.wim", []byte("install wim"))

	mounter := newFakeInstallMounter()
	windows := &WindowsISOManager{
		SourceRoots:    []string{smbRoot},
		MountRoot:      mountRoot,
		ConfigRoot:     configRoot,
		BootImageRoot:  bootRoot,
		InstallMounter: mounter,
		WIMCommands: &fakeWIMImageRunner{
			infoOut: map[string]string{"*": sampleBootWIMWithSetupInfo},
			dirOut:  map[string]string{"*": "/sources/setup.exe\n"},
		},
	}
	h := HandlerWithManagers(nil, nil, windows)

	// POST /windows/boot-image/prepare
	body, _ := json.Marshal(WindowsBootImageRequest{
		ID:           1,
		StorageRoot:  storage,
		RelativePath: "boot.wim",
		Size:         bootInfo.Size(),
		Modified:     bootInfo.ModTime(),
	})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/windows/boot-image/prepare", bytes.NewReader(body)))
	if res.Code != http.StatusOK {
		t.Fatalf("boot-image prepare status=%d body=%s", res.Code, res.Body.String())
	}
	var bootRes WindowsBootImageResult
	if err := json.Unmarshal(res.Body.Bytes(), &bootRes); err != nil || !bootRes.HasSetupExe || bootRes.SHA256 == "" {
		t.Fatalf("unexpected boot-image response: %+v err=%v", bootRes, err)
	}

	// POST /windows/install-image/prepare with boot.wim info -> 400 Bad Request
	instBody, _ := json.Marshal(WindowsInstallImageRequest{
		ID:           2,
		StorageRoot:  storage,
		RelativePath: "install.wim",
		Size:         instInfo.Size(),
		Modified:     instInfo.ModTime(),
	})
	resBad := httptest.NewRecorder()
	h.ServeHTTP(resBad, httptest.NewRequest(http.MethodPost, "/windows/install-image/prepare", bytes.NewReader(instBody)))
	if resBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for WinPE in install-image/prepare, got %d body=%s", resBad.Code, resBad.Body.String())
	}

	// Switch runner to install.wim info -> 200 OK
	windows.WIMCommands = &fakeWIMImageRunner{infoOut: map[string]string{"*": sampleInstallWIMInfo}}
	resOK := httptest.NewRecorder()
	h.ServeHTTP(resOK, httptest.NewRequest(http.MethodPost, "/windows/install-image/prepare", bytes.NewReader(instBody)))
	if resOK.Code != http.StatusOK {
		t.Fatalf("install-image prepare status=%d body=%s", resOK.Code, resOK.Body.String())
	}
	var instRes WindowsInstallImageResult
	if err := json.Unmarshal(resOK.Body.Bytes(), &instRes); err != nil || instRes.InstallFile != "install.wim" {
		t.Fatalf("unexpected install-image response: %+v err=%v", instRes, err)
	}

	// POST /windows/boot-image/remove & /windows/install-image/remove
	rmBoot := httptest.NewRecorder()
	h.ServeHTTP(rmBoot, httptest.NewRequest(http.MethodPost, "/windows/boot-image/remove", strings.NewReader(`{"id":1}`)))
	if rmBoot.Code != http.StatusNoContent {
		t.Fatalf("boot-image remove status=%d body=%s", rmBoot.Code, rmBoot.Body.String())
	}
	rmInst := httptest.NewRecorder()
	h.ServeHTTP(rmInst, httptest.NewRequest(http.MethodPost, "/windows/install-image/remove", strings.NewReader(`{"id":2}`)))
	if rmInst.Code != http.StatusNoContent {
		t.Fatalf("install-image remove status=%d body=%s", rmInst.Code, rmInst.Body.String())
	}
}
