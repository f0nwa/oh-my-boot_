package control

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
	"omb/internal/drivers"
	"omb/internal/images"
)

type driverMenuStore struct {
	windowsMenuPageStore
	configs []DriverConfig
}

func (s driverMenuStore) DriverConfigs(context.Context) ([]DriverConfig, error) {
	return s.configs, nil
}

type recordingDriverCheck struct {
	calls []agent.DriverBuildCheck
	fail  bool
}

func (c *recordingDriverCheck) PrepareDriverConfig(context.Context, agent.DriverBuildRequest) (agent.DriverBuildResult, error) {
	return agent.DriverBuildResult{}, nil
}
func (c *recordingDriverCheck) CheckDriverConfig(_ context.Context, check agent.DriverBuildCheck) error {
	c.calls = append(c.calls, check)
	if c.fail {
		return os.ErrNotExist
	}
	return nil
}

type recordingPackageCheck struct{ hashes []string }

func (c *recordingPackageCheck) ImportDriverPackage(context.Context, io.Reader, int64) (drivers.Inventory, error) {
	return drivers.Inventory{}, nil
}
func (c *recordingPackageCheck) CheckDriverPackage(_ context.Context, hash string) error {
	c.hashes = append(c.hashes, hash)
	return nil
}

func driverMenuFixture() (WindowsProfile, WindowsAnswerVariant, []DriverConfig) {
	profile := WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{StorageID: 7, StorageRoot: "/mnt/omb-smb/key7", Path: "windows.iso", Size: 123, Modified: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC), Title: "Windows 11", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"}
	answer := WindowsAnswerVariant{ID: 31, Title: "Lab", State: "ready", SHA256: strings.Repeat("d", 64), Size: 100}
	configs := []DriverConfig{
		{ID: 1, ProfileID: 17, Title: "Windows 11 Laptop A", Folder: "Windows/Laptops", State: "ready", PackageINFs: []agent.SelectedINF{{PackageSHA256: strings.Repeat("a", 64), INFPath: "net.inf"}}, Build: agent.DriverBuildResult{WIMKey: strings.Repeat("b", 64), WIMSHA256: strings.Repeat("c", 64), ScriptSHA256: strings.Repeat("e", 64)}},
		{ID: 2, ProfileID: 17, AnswerID: 31, Title: "Windows 11 Laptop B", Folder: "Windows/Laptops", State: "ready", PackageINFs: []agent.SelectedINF{{PackageSHA256: strings.Repeat("f", 64), INFPath: "storage.inf"}}, Build: agent.DriverBuildResult{WIMKey: strings.Repeat("1", 64), WIMSHA256: strings.Repeat("2", 64), ScriptSHA256: strings.Repeat("3", 64)}},
	}
	return profile, answer, configs
}

func TestMenuDistinguishesDuplicateDriverConfigTitles(t *testing.T) {
	profile, answer, configs := driverMenuFixture()
	configs[0].Title = "Windows 11 VirtIO Net SCSI test"
	configs[1].Title = configs[0].Title
	script, err := buildTestMenuFromDraft(MenuDraft{Title: "PXE", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 3001}, {InstallationID: 3002}}}, "http://10.30.0.10:8080", []WindowsProfile{profile}, []WindowsAnswerVariant{answer}, configs)
	if err != nil {
		t.Fatal(err)
	}
	store := driverMenuStore{windowsMenuPageStore: windowsMenuPageStore{
		menuPageStore: menuPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, versions: []MenuVersion{{Active: true, Title: "PXE", Label: "Diagnostics", Message: "Ready", Script: string(script), Items: []MenuVersionItem{{InstallationID: 3001, Title: configs[0].Title}, {InstallationID: 3002, Position: 1, Title: configs[1].Title}}}}},
		profiles:      []WindowsProfile{profile}, answers: []WindowsAnswerVariant{answer},
	}, configs: configs}
	s := &Server{Store: store}
	// Equal titles in the structure carry their keys; the selected item
	// names what it loads.
	req := httptest.NewRequest(http.MethodGet, "/menu?item=install_3001", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	for _, want := range []string{`<small class="mono">install_3001</small>`, `<small class="mono">install_3002</small>`, "Образ с драйверами"} {
		if !strings.Contains(res.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestDriverMenuRendersSeparateEntriesAndFolders(t *testing.T) {
	profile, answer, configs := driverMenuFixture()
	form := url.Values{"title": {"oh my boot_"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_1017": {"on"}, "install_3001": {"on"}, "install_3002": {"on"}}
	script, err := buildTestMenuWithDrivers(form, "http://10.30.0.10:8080", []WindowsProfile{profile}, []WindowsAnswerVariant{answer}, configs)
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, want := range []string{
		"item install_3001 Windows 11 Laptop A${sp",
		"item install_3002 Windows 11 Laptop B${sp",
		"item menu_root .. Back\n",
		"initrd http://10.30.0.10:8080/assets/windows-driver-wims/" + configs[0].Build.WIMKey + "/boot.wim boot.wim\n",
		"initrd -n omb-setup.cmd http://10.30.0.10:8080/assets/windows-driver-configs/2/omb-setup.cmd\n",
		"initrd -n omb-answer.xml http://10.30.0.10:8080/assets/windows-answers/31/omb-answer.xml\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q", want)
		}
	}
	refs := menuDriverConfigRefs(text)
	if len(refs) != 2 || refs[2].AnswerSHA256 != answer.SHA256 || menuFolderPaths(text)["install_3001"] != "Windows/Laptops" {
		t.Fatalf("bad marker: %+v", refs)
	}
}

func TestDriverMenuVerifierRejectsMissingOrChangedAssets(t *testing.T) {
	profile, answer, configs := driverMenuFixture()
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_3001": {"on"}}
	script, err := buildTestMenuWithDrivers(form, "http://10.30.0.10:8080", []WindowsProfile{profile}, []WindowsAnswerVariant{answer}, configs)
	if err != nil {
		t.Fatal(err)
	}
	store := driverMenuStore{windowsMenuPageStore: windowsMenuPageStore{profiles: []WindowsProfile{profile}, answers: []WindowsAnswerVariant{answer}}, configs: configs}
	iso := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 7, Kind: "smb", Root: profile.StorageRoot, State: "ready"}}, Images: []images.Image{{StorageID: 7, Path: profile.Path, Size: profile.Size, Modified: profile.Modified, Present: true}}}}
	check := &recordingDriverCheck{}
	pkg := &recordingPackageCheck{}
	s := &Server{Store: store, Images: iso, WindowsISO: &recordingWindowsISOAPI{}, DriverConfigs: check, DriverPackages: pkg}
	if err := s.verifyMenuWindowsProfiles(context.Background(), string(script), menuProfile{windowsISOID: 17, driverConfigID: 1}); err != nil || len(check.calls) != 1 || len(pkg.hashes) != 1 {
		t.Fatalf("valid entry rejected: %v %+v %+v", err, check.calls, pkg.hashes)
	}
	forged := strings.Replace(string(script), menuDriverMarker, "# removed-marker ", 1)
	if err := s.verifyMenuWindowsProfiles(context.Background(), forged, menuProfile{windowsISOID: 17, driverConfigID: 1}); err == nil {
		t.Fatal("accepted missing driver marker")
	}
	check.fail = true
	if err := s.verifyMenuWindowsProfiles(context.Background(), string(script), menuProfile{windowsISOID: 17, driverConfigID: 1}); err == nil {
		t.Fatal("accepted missing derived WIM")
	}
	check.fail = false
	store.configs[0].Build.WIMSHA256 = strings.Repeat("9", 64)
	if err := s.verifyMenuWindowsProfiles(context.Background(), string(script), menuProfile{windowsISOID: 17, driverConfigID: 1}); err == nil {
		t.Fatal("accepted changed derived WIM hash")
	}
}

func TestDriverMenuVerifierChecksAnswerVariant(t *testing.T) {
	profile, answer, configs := driverMenuFixture()
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_3002": {"on"}}
	script, err := buildTestMenuWithDrivers(form, "http://10.30.0.10:8080", []WindowsProfile{profile}, []WindowsAnswerVariant{answer}, configs)
	if err != nil {
		t.Fatal(err)
	}
	store := driverMenuStore{windowsMenuPageStore: windowsMenuPageStore{profiles: []WindowsProfile{profile}, answers: []WindowsAnswerVariant{answer}}, configs: configs}
	iso := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 7, Kind: "smb", Root: profile.StorageRoot, State: "ready"}}, Images: []images.Image{{StorageID: 7, Path: profile.Path, Size: profile.Size, Modified: profile.Modified, Present: true}}}}
	s := &Server{Store: store, Images: iso, WindowsISO: &recordingWindowsISOAPI{}, WindowsAnswers: &answerAgent{}, DriverConfigs: &recordingDriverCheck{}, DriverPackages: &recordingPackageCheck{}}
	if err := s.verifyMenuWindowsProfiles(context.Background(), string(script), menuProfile{windowsISOID: 17, driverConfigID: 2}); err != nil {
		t.Fatal(err)
	}
	store.answers[0].SHA256 = strings.Repeat("8", 64)
	if err := s.verifyMenuWindowsProfiles(context.Background(), string(script), menuProfile{windowsISOID: 17, driverConfigID: 2}); err == nil {
		t.Fatal("accepted changed answer XML")
	}
}

func (s driverMenuStore) Installations(context.Context) ([]Installation, error) {
	return testInstallationSources(s.profiles, s.answers, s.configs).Installations, nil
}
func (s driverMenuStore) BootProfiles(context.Context) ([]BootProfile, error) {
	return testInstallationSources(s.profiles, s.answers, s.configs).BootProfiles, nil
}
