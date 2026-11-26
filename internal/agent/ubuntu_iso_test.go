package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omb/internal/boot"
)

type recordingUbuntuISO struct{ calls int }

type recordingUbuntuPin struct {
	recordingUbuntuISO
	pins int
}

func (m *recordingUbuntuPin) PinLegacy(_ context.Context, id int64, digest string) error {
	m.pins++
	return nil
}

func TestUbuntuLegacyPinAPIRejectsUnsafeIdentity(t *testing.T) {
	manager := &recordingUbuntuPin{}
	h := HandlerWithUbuntuISO(http.NotFoundHandler(), manager)
	for _, body := range []string{`{"id":0,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`, `{"id":8,"sha256":"bad"}`, `{"id":8,"sha256":"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"}`} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/ubuntu-iso/pin-legacy", strings.NewReader(body)))
		if res.Code != http.StatusBadRequest || manager.pins != 0 {
			t.Fatalf("status=%d pins=%d", res.Code, manager.pins)
		}
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/ubuntu-iso/pin-legacy", strings.NewReader(`{"id":8,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)))
	if res.Code != http.StatusNoContent || manager.pins != 1 {
		t.Fatalf("status=%d pins=%d", res.Code, manager.pins)
	}
}

type ubuntuInspectMounter struct {
	info, sources string
	unmounted     bool
}

func (m *ubuntuInspectMounter) IsMounted(context.Context, string) (bool, error) { return false, nil }
func (m *ubuntuInspectMounter) Mount(_ context.Context, _ string, target string) error {
	for name, data := range map[string]string{
		".disk/info": m.info, "casper/install-sources.yaml": m.sources,
		"casper/vmlinuz": "kernel", "casper/initrd": "initrd", "EFI/boot/bootx64.efi": "shim",
	} {
		file := filepath.Join(target, name)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(file, []byte(data), 0644); err != nil {
			return err
		}
	}
	return nil
}
func (m *ubuntuInspectMounter) Unmount(context.Context, string) error { m.unmounted = true; return nil }

type preparedUbuntuRunner struct {
	assetRoot string
	calls     int
	corrupt   bool
}

func (r *preparedUbuntuRunner) Run(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
	r.calls++
	values := map[string]string{}
	for i := 0; i+1 < len(args); i += 2 {
		values[args[i]] = args[i+1]
	}
	dir := filepath.Join(r.assetRoot, "ubuntu-sources", values["--source-id"], values["--sha256"])
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, name := range []string{"vmlinuz", "initrd", "bootx64.efi", "install-sources.yaml"} {
		data := []byte("artifact")
		if name == "install-sources.yaml" {
			data = []byte("- id: ubuntu-desktop\n  name: Desktop\n")
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			return nil, err
		}
		files[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	info, err := os.Stat(values["--iso"])
	if err != nil {
		return nil, err
	}
	digest := values["--sha256"]
	if r.corrupt {
		digest = strings.Repeat("b", 64)
	}
	manifest, err := json.Marshal(map[string]any{"version": 1, "iso_sha256": digest, "iso_size": info.Size(), "files": files})
	if err != nil {
		return nil, err
	}
	return nil, os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0644)
}

func (m *recordingUbuntuISO) Prepare(context.Context, UbuntuISORequest) error {
	m.calls++
	return nil
}

func (m *recordingUbuntuISO) Inspect(context.Context, WindowsISORequest) (UbuntuISOInspection, error) {
	m.calls++
	return UbuntuISOInspection{SHA256: strings.Repeat("a", 64)}, nil
}

func TestUbuntuISOManagerValidatesManagedSourceAndDigest(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "nas")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(storage, "ubuntu.iso")
	contents := []byte("test Ubuntu ISO")
	if err := os.WriteFile(file, contents, 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	manager := &UbuntuISOManager{SourceRoot: base}
	req := UbuntuISORequest{WindowsISORequest: WindowsISORequest{ID: 8, StorageRoot: storage, RelativePath: "ubuntu.iso", Size: info.Size(), Modified: info.ModTime()}, SHA256: fmt.Sprintf("%x", sha256.Sum256(contents))}
	expected, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := manager.Validate(context.Background(), req); err != nil || got != expected {
		t.Fatalf("valid source=%q err=%v", got, err)
	}
	bad := req
	bad.SHA256 = strings.Repeat("b", 64)
	if _, err := manager.Validate(context.Background(), bad); err == nil {
		t.Fatal("accepted incorrect digest")
	}
	bad = req
	bad.RelativePath = "../ubuntu.iso"
	if _, err := manager.Validate(context.Background(), bad); err == nil {
		t.Fatal("accepted path outside storage")
	}
	link := filepath.Join(storage, "link.iso")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	bad = req
	bad.RelativePath = "link.iso"
	if _, err := manager.Validate(context.Background(), bad); err == nil {
		t.Fatal("accepted symlink source")
	}
}

func TestUbuntuISOInspectionRejectsUnsupportedMedia(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "nas")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(storage, "ubuntu.iso")
	data := []byte("ISO bytes")
	if err := os.WriteFile(file, data, 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	req := WindowsISORequest{ID: 6, StorageRoot: storage, RelativePath: "ubuntu.iso", Size: info.Size(), Modified: info.ModTime()}
	mount := &ubuntuInspectMounter{info: "Ubuntu 26.04 - Release amd64", sources: "- id: ubuntu-desktop\n  name: Desktop\n"}
	manager := &UbuntuISOManager{SourceRoot: base, Mounter: mount}
	result, err := manager.Inspect(context.Background(), req)
	if err != nil || result.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) || len(result.Sources) != 1 || !mount.unmounted {
		t.Fatalf("valid Desktop ISO: result=%+v err=%v", result, err)
	}
	for _, wrong := range []struct{ info, sources string }{
		{"Ubuntu Server - Release amd64", "- id: ubuntu-server\n  name: Server\n"},
		{"Ubuntu Desktop - Release arm64", "- id: ubuntu-desktop\n  name: Desktop\n"},
		{"Ubuntu Desktop - Release amd64", "invalid: ["},
	} {
		mount.info, mount.sources, mount.unmounted = wrong.info, wrong.sources, false
		if _, err := manager.Inspect(context.Background(), req); err == nil || !mount.unmounted {
			t.Fatalf("accepted unsupported ISO: info=%q err=%v", wrong.info, err)
		}
	}
}

func TestUbuntuISOManagerPreparesOnlyValidatedSource(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "nas")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(storage, "ubuntu.iso")
	contents := []byte("test Ubuntu ISO")
	if err := os.WriteFile(file, contents, 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	runner := &preparedUbuntuRunner{assetRoot: t.TempDir()}
	registry := t.TempDir()
	manager := &UbuntuISOManager{SourceRoot: base, AssetRoot: runner.assetRoot, ScriptPath: "/usr/local/libexec/omb-prepare-ubuntu-source", Runner: runner, RegistryRoot: registry}
	req := UbuntuISORequest{WindowsISORequest: WindowsISORequest{ID: 8, StorageRoot: storage, RelativePath: "ubuntu.iso", Size: info.Size(), Modified: info.ModTime()}, SHA256: fmt.Sprintf("%x", sha256.Sum256(contents))}
	if err := manager.Prepare(context.Background(), req); err != nil || runner.calls != 1 {
		t.Fatalf("valid source: calls=%d err=%v", runner.calls, err)
	}
	if _, err := os.Stat(filepath.Join(registry, "8.json")); err != nil {
		t.Fatalf("prepared source was not registered with Boot: %v", err)
	}
	t.Setenv("OMB_BOOT_UBUNTU_REGISTRY", registry)
	publisher, err := boot.NewPublisher(filepath.Join(t.TempDir(), "menu.ipxe"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	bootHandler, err := boot.NewDynamicHandlerWithISO(runner.assetRoot, publisher, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/images/ubuntu/8/"+req.SHA256+".iso", nil)
	request.RemoteAddr = "10.30.0.20:1234"
	bootHandler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != string(contents) {
		t.Fatalf("prepared ISO not available from Boot: status=%d", response.Code)
	}
	bad := req
	bad.SHA256 = strings.Repeat("b", 64)
	if err := manager.Prepare(context.Background(), bad); err == nil || runner.calls != 1 {
		t.Fatalf("incorrect digest reached preparation: calls=%d err=%v", runner.calls, err)
	}
	runner.corrupt = true
	if err := manager.Prepare(context.Background(), req); err == nil {
		t.Fatal("accepted corrupt prepared manifest")
	}
}

func TestPinLegacyUbuntuRequiresMatchingAssetsAndCannotBeChanged(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "nas")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	iso := filepath.Join(storage, "ubuntu.iso")
	data := []byte("test Ubuntu ISO")
	if err := os.WriteFile(iso, data, 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(iso)
	if err != nil {
		t.Fatal(err)
	}
	runner := &preparedUbuntuRunner{assetRoot: t.TempDir()}
	manager := &UbuntuISOManager{SourceRoot: base, AssetRoot: runner.assetRoot, ScriptPath: "/usr/local/libexec/omb-prepare-ubuntu-source", Runner: runner, RegistryRoot: t.TempDir()}
	legacyConfig := filepath.Join(base, "boot.env")
	if err := os.WriteFile(legacyConfig, []byte("OMB_BOOT_UBUNTU_ISO="+iso+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager.LegacyConfigPath = legacyConfig
	req := UbuntuISORequest{WindowsISORequest: WindowsISORequest{ID: 8, StorageRoot: storage, RelativePath: "ubuntu.iso", Size: info.Size(), Modified: info.ModTime()}, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	if err := manager.Prepare(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(runner.assetRoot, "ubuntu")
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"vmlinuz", "initrd", "bootx64.efi"} {
		if err := os.WriteFile(filepath.Join(legacy, name), []byte("artifact"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	otherISO := filepath.Join(storage, "other.iso")
	otherData := []byte("another Ubuntu ISO")
	if err := os.WriteFile(otherISO, otherData, 0644); err != nil {
		t.Fatal(err)
	}
	otherInfo, err := os.Stat(otherISO)
	if err != nil {
		t.Fatal(err)
	}
	other := UbuntuISORequest{WindowsISORequest: WindowsISORequest{ID: 9, StorageRoot: storage, RelativePath: "other.iso", Size: otherInfo.Size(), Modified: otherInfo.ModTime()}, SHA256: fmt.Sprintf("%x", sha256.Sum256(otherData))}
	if err := manager.Prepare(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if err := manager.PinLegacy(t.Context(), 9, other.SHA256); err == nil {
		t.Fatal("pinned a different ISO sharing the same boot artifacts")
	}
	if err := os.WriteFile(filepath.Join(legacy, "initrd"), []byte("different"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := manager.PinLegacy(t.Context(), 8, req.SHA256); err == nil {
		t.Fatal("pinned different legacy assets")
	}
	if _, err := os.Stat(filepath.Join(manager.RegistryRoot, "legacy.json")); !os.IsNotExist(err) {
		t.Fatal("failure created legacy alias")
	}
	if err := os.WriteFile(filepath.Join(legacy, "initrd"), []byte("artifact"), 0644); err != nil {
		t.Fatal(err)
	}
	oldInitrd := filepath.Join(legacy, "initrd")
	if err := os.Remove(oldInitrd); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(legacy, "vmlinuz"), oldInitrd); err != nil {
		t.Fatal(err)
	}
	if err := manager.PinLegacy(t.Context(), 8, req.SHA256); err == nil {
		t.Fatal("pinned a symlinked legacy asset")
	}
	if err := os.Remove(oldInitrd); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldInitrd, []byte("artifact"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := manager.PinLegacy(t.Context(), 8, req.SHA256); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(manager.RegistryRoot, "legacy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.PinLegacy(t.Context(), 8, req.SHA256); err != nil {
		t.Fatalf("repeat pin failed: %v", err)
	}
	if err := manager.PinLegacy(t.Context(), 8, strings.Repeat("b", 64)); err == nil {
		t.Fatal("changed legacy alias")
	}
	after, _ := os.ReadFile(filepath.Join(manager.RegistryRoot, "legacy.json"))
	if string(after) != string(before) {
		t.Fatal("legacy alias was overwritten")
	}
}

func TestUbuntuISOPrepareFailsClosedWithoutManager(t *testing.T) {
	h := HandlerWithManagers(nil, nil, nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/ubuntu-iso/prepare", strings.NewReader(`{}`)))
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured Ubuntu manager status=%d", res.Code)
	}
}

func TestUbuntuISORejectsUnsafeRequestBeforePreparation(t *testing.T) {
	manager := &recordingUbuntuISO{}
	h := HandlerWithUbuntuISO(http.NotFoundHandler(), manager)
	for _, body := range []string{
		`{"id":0,"storage_root":"/storage","relative_path":"x.iso","size":5,"modified":"2026-11-04T00:00:00Z","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		`{"id":1,"storage_root":"/storage","relative_path":"../x.iso","size":5,"modified":"2026-11-04T00:00:00Z","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		`{"id":1,"storage_root":"/storage","relative_path":"x.iso","size":5,"modified":"2026-11-04T00:00:00Z","sha256":"bad"}`,
	} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/ubuntu-iso/prepare", strings.NewReader(body)))
		if res.Code != http.StatusBadRequest || manager.calls != 0 {
			t.Fatalf("unsafe request: status=%d calls=%d", res.Code, manager.calls)
		}
	}
	res := httptest.NewRecorder()
	body := `{"id":1,"storage_root":"/storage","relative_path":"x.iso","size":5,"modified":"2026-11-04T00:00:00Z","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/ubuntu-iso/prepare", strings.NewReader(body)))
	if res.Code != http.StatusNoContent || manager.calls != 1 {
		t.Fatalf("valid request: status=%d calls=%d", res.Code, manager.calls)
	}
}

func TestUbuntuISOInspectionAPI(t *testing.T) {
	manager := &recordingUbuntuISO{}
	h := HandlerWithUbuntuISO(http.NotFoundHandler(), manager)
	body := `{"id":6,"storage_root":"/storage","relative_path":"x.iso","size":5,"modified":"2026-11-04T00:00:00Z"}`
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/ubuntu-iso/inspect", strings.NewReader(body)))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), strings.Repeat("a", 64)) || manager.calls != 1 {
		t.Fatalf("inspection status=%d calls=%d", res.Code, manager.calls)
	}
}

func TestISOInspectionDetectsWindowsAndUbuntu(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "nas")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(storage, "renamed.iso")
	if err := os.WriteFile(file, []byte("ISO"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	req := WindowsISORequest{ID: 6, StorageRoot: storage, RelativePath: "renamed.iso", Size: info.Size(), Modified: info.ModTime()}
	for _, tc := range []struct {
		name, kind string
		mounter    WindowsISOMounter
	}{
		{"Windows", "windows", &fakeWindowsISOMounter{}},
		{"Strelec", "strelec", strelecMounterFixture{}},
		{"non-directory efi entry", "unsupported", &isoWithNonDirectoryEFI{}},
		{"unsupported Ubuntu ARM64", "unsupported", &ubuntuInspectMounter{info: "Ubuntu Desktop - Release arm64", sources: "- id: ubuntu-desktop\n  name: Desktop\n"}},
		{"Ubuntu", "ubuntu", &ubuntuInspectMounter{info: "Ubuntu 26.04 - Release amd64", sources: "- id: ubuntu-desktop\n  name: Desktop\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &UbuntuISOManager{SourceRoot: base, Mounter: tc.mounter}
			got, err := manager.InspectISO(context.Background(), req)
			if err != nil || got.Kind != tc.kind {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			if tc.kind == "ubuntu" && (got.Version != "26.04" || got.Arch != "x64") {
				t.Fatalf("expected ubuntu version 26.04 / x64, got %+v", got)
			}
		})
	}
}

func TestParseUbuntuDiskInfoAndWIMInspectionMetadata(t *testing.T) {
	ver, arch, sum := parseUbuntuDiskInfo(`Ubuntu 24.04.3 LTS "Noble Numbat" - Release amd64 (20250805.1)`)
	if ver != "24.04.3 LTS" || arch != "x64" || !strings.Contains(sum, "Noble Numbat") {
		t.Fatalf("parseUbuntuDiskInfo = (%q, %q, %q)", ver, arch, sum)
	}
	var res ISOInspection
	fillWIMInspectionMetadata(&res, WindowsWIMInfo{
		Known: true,
		Images: []WindowsWIMImage{
			{Index: 1, Name: "Windows 11 Home", Architecture: "amd64", Build: 26100},
			{Index: 2, Name: "Windows 11 Pro", Architecture: "amd64", Build: 26100},
		},
	}, "install.wim")
	if res.Version != "10.0.26100" || res.Arch != "x64" || !strings.Contains(res.Summary, "Windows 11 Pro") || !strings.Contains(res.Summary, "install.wim") {
		t.Fatalf("fillWIMInspectionMetadata = %+v", res)
	}
}

type changedISOInspectionMounter struct {
	fakeWindowsISOMounter
	file string
}

func (m *changedISOInspectionMounter) Mount(ctx context.Context, source, target string) error {
	if err := m.fakeWindowsISOMounter.Mount(ctx, source, target); err != nil {
		return err
	}
	return os.WriteFile(m.file, []byte("replacement image with a different size"), 0600)
}
func TestWindowsISOInspectionRejectsReplacementAndUnmounts(t *testing.T) {
	base := t.TempDir()
	storage := filepath.Join(base, "nas")
	if err := os.Mkdir(storage, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(storage, "renamed.iso")
	if err := os.WriteFile(file, []byte("ISO"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	mounter := &changedISOInspectionMounter{file: file}
	manager := &UbuntuISOManager{SourceRoot: base, Mounter: mounter}
	_, err = manager.InspectISO(context.Background(), WindowsISORequest{ID: 7, StorageRoot: storage, RelativePath: "renamed.iso", Size: info.Size(), Modified: info.ModTime()})
	if err == nil || !mounter.undo {
		t.Fatalf("changed source accepted or mount leaked: err=%v unmounted=%v", err, mounter.undo)
	}
}

type unreadableISOInspectionMounter struct{ ubuntuInspectMounter }

func (m *unreadableISOInspectionMounter) Mount(ctx context.Context, source, target string) error {
	if err := m.ubuntuInspectMounter.Mount(ctx, source, target); err != nil {
		return err
	}
	return os.Chmod(filepath.Join(target, ".disk/info"), 0000)
}
func TestGenericISOInspectionPreservesMetadataReadErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission test requires an unprivileged user")
	}
	base := t.TempDir()
	storage := filepath.Join(base, "nas")
	if err := os.Mkdir(storage, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(storage, "test.iso")
	if err := os.WriteFile(file, []byte("ISO"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	mounter := &unreadableISOInspectionMounter{ubuntuInspectMounter: ubuntuInspectMounter{info: "Ubuntu Desktop - Release amd64", sources: "- id: ubuntu-desktop\n  name: Desktop\n"}}
	manager := &UbuntuISOManager{SourceRoot: base, Mounter: mounter}
	result, err := manager.InspectISO(context.Background(), WindowsISORequest{ID: 7, StorageRoot: storage, RelativePath: "test.iso", Size: info.Size(), Modified: info.ModTime()})
	if err == nil || result.Kind == "unsupported" {
		t.Fatalf("read error classified as unsupported: result=%+v err=%v", result, err)
	}
}

type isoWithNonDirectoryEFI struct{ ubuntuInspectMounter }

func (m *isoWithNonDirectoryEFI) Mount(_ context.Context, _ string, target string) error {
	return os.WriteFile(filepath.Join(target, "efi"), []byte("not a directory"), 0600)
}

func TestUbuntuISOManagerAcceptsNFSSourceRoots(t *testing.T) {
	smbRoot, nfsRoot := t.TempDir(), t.TempDir()
	storage := filepath.Join(nfsRoot, "nfskey")
	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(storage, "ubuntu.iso")
	if err := os.WriteFile(file, []byte("UbuntuISOData"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	mounter := &ubuntuInspectMounter{
		info:    "Ubuntu Desktop - Release amd64",
		sources: "- id: ubuntu-desktop\n  name: Desktop\n",
	}
	manager := &UbuntuISOManager{
		SourceRoots: []string{smbRoot, nfsRoot},
		Mounter:     mounter,
	}
	req := WindowsISORequest{
		ID:           12,
		StorageRoot:  storage,
		RelativePath: "ubuntu.iso",
		Size:         info.Size(),
		Modified:     info.ModTime(),
	}
	inspection, err := manager.InspectISO(context.Background(), req)
	if err != nil {
		t.Fatalf("InspectISO failed: %v", err)
	}
	if inspection.Kind != "ubuntu" {
		t.Fatalf("inspection.Kind = %q, want ubuntu", inspection.Kind)
	}
}
