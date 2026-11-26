package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"omb/internal/agent"
	"omb/internal/images"
)

type unifiedMenuStore struct {
	*draftStore
	installations []Installation
	bootProfiles  []BootProfile
	bootImages    []WindowsBootImage
	installImages []WindowsInstallImage
}

func (s *unifiedMenuStore) Installations(context.Context) ([]Installation, error) {
	return s.installations, nil
}
func (s *unifiedMenuStore) BootProfiles(context.Context) ([]BootProfile, error) {
	return s.bootProfiles, nil
}
func (s *unifiedMenuStore) ListWindowsBootImages(context.Context) ([]WindowsBootImage, error) {
	return s.bootImages, nil
}
func (s *unifiedMenuStore) GetWindowsBootImage(_ context.Context, id int64) (WindowsBootImage, error) {
	for _, img := range s.bootImages {
		if img.ID == id {
			return img, nil
		}
	}
	return WindowsBootImage{}, fmt.Errorf("boot image %d not found", id)
}
func (s *unifiedMenuStore) UpsertFileBootImage(context.Context, WindowsBootImageSource) (WindowsBootImage, error) {
	return WindowsBootImage{}, nil
}
func (s *unifiedMenuStore) SetBootImageReady(context.Context, int64, string, bool, agent.WindowsWIMInfo) error {
	return nil
}
func (s *unifiedMenuStore) SetBootImageError(context.Context, int64, string, string) error {
	return nil
}
func (s *unifiedMenuStore) SetBootImageTitle(context.Context, int64, string) error { return nil }
func (s *unifiedMenuStore) DeleteWindowsBootImage(context.Context, int64) error    { return nil }
func (s *unifiedMenuStore) ListWindowsInstallImages(context.Context) ([]WindowsInstallImage, error) {
	return s.installImages, nil
}
func (s *unifiedMenuStore) GetWindowsInstallImage(_ context.Context, id int64) (WindowsInstallImage, error) {
	for _, img := range s.installImages {
		if img.ID == id {
			return img, nil
		}
	}
	return WindowsInstallImage{}, fmt.Errorf("install image %d not found", id)
}
func (s *unifiedMenuStore) UpsertWindowsInstallImage(context.Context, WindowsInstallImageSource) (WindowsInstallImage, error) {
	return WindowsInstallImage{}, nil
}
func (s *unifiedMenuStore) SetInstallImageReady(context.Context, int64, string, agent.WindowsWIMInfo) error {
	return nil
}
func (s *unifiedMenuStore) SetInstallImageError(context.Context, int64, string, string) error {
	return nil
}
func (s *unifiedMenuStore) SetInstallImageTitle(context.Context, int64, string) error { return nil }
func (s *unifiedMenuStore) DeleteWindowsInstallImage(context.Context, int64) error    { return nil }

func unifiedMenuFixture(t *testing.T) (*Server, *unifiedMenuStore, *[]string) {
	t.Helper()
	base := newDraftStore(t, "operator", nil)
	base.draft = &MenuDraft{Title: productMenuTitle, Label: "Tools", Message: "Ready", NoDiagnostics: true, NoLocalTimer: true, LocalTimeout: 23, Items: []MenuDraftItem{
		{InstallationID: 71, Title: "Ubuntu Lab", Folder: "Linux/Lab"},
		{InstallationID: 72, Title: "Windows Lab", Folder: "Windows"},
		{InstallationID: 73, Title: "Ubuntu Recovery", Folder: "Recovery"},
	}}
	store := &unifiedMenuStore{draftStore: base, bootProfiles: []BootProfile{{ID: 1, Kind: BootProfileUbuntuBuiltin, Title: "Ubuntu"}, {ID: 2, Kind: BootProfileWindowsISO, WindowsProfileID: 17, Title: "Windows"}}, installations: []Installation{{ID: 71, BootProfileID: 1, Title: "Ubuntu"}, {ID: 72, BootProfileID: 2, Title: "Windows"}, {ID: 73, BootProfileID: 1, Title: "Ubuntu"}}}
	s, published := draftServer(t, base)
	s.Store = store
	s.UbuntuReadiness = HTTPUbuntuReadiness{BaseURL: ubuntuBootResources(t, "")}
	return s, store, published
}

// Catches collapsing Ubuntu installations into a flag and rendering legacy keys.
func TestUnifiedMenuPublishesOrderedInstallationsAndStructure(t *testing.T) {
	s, store, published := unifiedMenuFixture(t)
	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther || len(*published) != 1 {
		t.Fatalf("publish=%d: %s", res.Code, res.Body.String())
	}
	script := (*published)[0]
	for _, want := range []string{
		"item install_71 Ubuntu Lab\n", "item install_72 Windows Lab\n", "item install_73 Ubuntu Recovery\n",
		"kernel " + testBootURL + "/assets/ubuntu/vmlinuz initrd=initrd boot=casper ip=dhcp iso-url=" + testBootURL + "/images/ubuntu-desktop.iso --- || goto install_71_failed\n",
		"initrd " + testBootURL + "/assets/ubuntu/initrd || goto install_71_failed\n",
		"iseq ${platform} efi && goto install_73_efi || goto install_73_boot\n",
		"shim " + testBootURL + "/assets/ubuntu/bootx64.efi || goto install_73_failed\n",
		"kernel " + testBootURL + "/assets/windows/wimboot || goto install_72_failed\n",
		"initrd " + testBootURL + "/assets/windows-profiles/17/boot.wim boot.wim || goto install_72_failed\n",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Index(script, ":install_71\n") > strings.Index(script, ":install_72\n") || strings.Index(script, ":install_72\n") > strings.Index(script, ":install_73\n") {
		t.Fatal("installation boot order changed")
	}
	for _, key := range []string{"install_71", "install_73"} {
		if strings.Count(script, ":"+key+"_failed\n") != 1 {
			t.Errorf("missing unique failure label for %s", key)
		}
	}
	version := store.versions[0]
	wantItems := []MenuVersionItem{{InstallationID: 71, Position: 0, Title: "Ubuntu Lab", Folder: "Linux/Lab"}, {InstallationID: 72, Position: 1, Title: "Windows Lab", Folder: "Windows"}, {InstallationID: 73, Position: 2, Title: "Ubuntu Recovery", Folder: "Recovery"}}
	if !reflect.DeepEqual(version.Items, wantItems) || !version.NoDiagnostics || !version.NoLocalTimer || version.LocalTimeout != 23 || version.Look != menuLook {
		t.Fatalf("version lost structure/settings: %+v", version)
	}
	if len(diffMenuDraft(draftFromVersion(version), *store.draft)) != 0 {
		t.Fatalf("publication leaves pending changes: %+v", store.draft)
	}
	// The saved script remains authoritative on restoration, while structure comes from Items.
	store.versions[0].Script = "#!ipxe\n# saved snapshot\n"
	store.draft.Items = nil
	res = menuPost(t, s, "/menu/rollback", url.Values{"id": {"100"}})
	if res.Code != http.StatusSeeOther || len(*published) != 2 || (*published)[1] != "#!ipxe\n# saved snapshot\n" || !reflect.DeepEqual(store.draft.Items, []MenuDraftItem{{InstallationID: 71, Title: "Ubuntu Lab", Folder: "Linux/Lab"}, {InstallationID: 72, Title: "Windows Lab", Folder: "Windows"}, {InstallationID: 73, Title: "Ubuntu Recovery", Folder: "Recovery"}}) {
		t.Fatalf("restore=%d draft=%+v published=%v", res.Code, store.draft, *published)
	}
}

func TestUnifiedEmptyPublicationKeepsExplicitStructure(t *testing.T) {
	s, store, _ := unifiedMenuFixture(t)
	store.draft.Items = nil
	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("publish=%d %s", res.Code, res.Body.String())
	}
	if store.versions[0].Items == nil || store.versions[0].Look != menuLook || !store.versions[0].NoDiagnostics || !store.versions[0].NoLocalTimer {
		t.Fatalf("empty publication lacks metadata: %+v", store.versions[0])
	}
}

func TestUnifiedPreviewAndEditorUseInstallationIdentity(t *testing.T) {
	s, store, published := unifiedMenuFixture(t)
	body := libraryGet(t, s, "/menu?item=install_73")
	for _, want := range []string{`action="/menu/items/install_73/title"`, `value="Ubuntu Recovery"`, `value="Recovery"`, `data-key="install_73"`} {
		if !strings.Contains(body, want) {
			t.Errorf("preview missing %s", want)
		}
	}
	for _, edit := range []struct {
		path string
		form url.Values
	}{
		{"/menu/items/install_73/move", url.Values{"dir": {"up"}}},
		{"/menu/items/install_73/title", url.Values{"title": {"Recovery Tools"}, "folder": {"Linux/Rescue"}}},
		{"/menu/order", url.Values{"key": {"install_72", "install_73", "install_71"}}},
	} {
		res := menuPost(t, s, edit.path, edit.form)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("edit %s: %d %s", edit.path, res.Code, res.Body.String())
		}
	}
	if store.draft.Items[1].InstallationID != 73 || store.draft.Items[1].Title != "Recovery Tools" || store.draft.Items[1].Folder != "Linux/Rescue" || len(*published) != 0 {
		t.Fatalf("edited draft=%+v published=%v", store.draft, *published)
	}
}

// A snapshot must remain unavailable when an installation or its source disappears,
// even if the saved script no longer exposes a recognizable legacy key.
func TestUnifiedRestoreChecksStructuredReferences(t *testing.T) {
	for _, missing := range []string{"installation", "boot_profile", "windows_iso"} {
		t.Run(missing, func(t *testing.T) {
			s, store, published := unifiedMenuFixture(t)
			store.versions = []MenuVersion{{ID: 44, Script: "#!ipxe\n# opaque saved script\n", Title: productMenuTitle, Label: "Tools", Message: "Ready", Look: menuLook, Items: []MenuVersionItem{{InstallationID: 72, Title: "Windows Lab"}}}}
			switch missing {
			case "installation":
				store.installations = nil
			case "boot_profile":
				store.bootProfiles = nil
			case "windows_iso":
				store.profiles = nil
			}
			res := menuPost(t, s, "/menu/rollback", url.Values{"id": {"44"}})
			if res.Code < 400 || len(*published) != 0 || len(store.draft.Items) != 3 {
				t.Fatalf("restored missing %s: status=%d publishes=%d draft=%+v", missing, res.Code, len(*published), store.draft)
			}
			body := libraryGet(t, s, "/menu")
			if strings.Contains(body, `name="id" value="44"`) {
				t.Fatal("unavailable version offered for restoration")
			}
		})
	}
}

// Installation identity must retain the Windows option boot path and its checks.
func TestUnifiedWindowsAnswerPublicationKeepsChecks(t *testing.T) {
	s, store, published := unifiedMenuFixture(t)
	store.installations[1].AnswerID = 31
	store.draft.Items = []MenuDraftItem{{InstallationID: 72, Title: "Windows Auto"}}
	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther || len(*published) != 1 {
		t.Fatalf("answer publish=%d %s", res.Code, res.Body.String())
	}
	if !strings.Contains((*published)[0], "/assets/windows-answers/31/"+store.answers[0].SHA256+"/omb-answer.xml") || !strings.Contains((*published)[0], "goto install_72_failed") {
		t.Fatal("answer boot path lost installation identity")
	}
	store.answers[0].SHA256 = strings.Repeat("f", 64)
	res = menuPost(t, s, "/menu/rollback", url.Values{"id": {"100"}})
	if res.Code < 400 || len(*published) != 1 {
		t.Fatalf("restored changed XML: %d publishes=%d", res.Code, len(*published))
	}
}

func TestUnifiedPreviewShowsProfileAndComponents(t *testing.T) {
	s, store, _ := unifiedMenuFixture(t)
	store.installations[1].DriverConfigID = 2
	for _, tc := range []struct{ key, kind, image string }{{"install_71", "Ubuntu", "Ubuntu"}, {"install_72", "Образ с драйверами", "Windows 11"}} {
		body := libraryGet(t, s, "/menu?item="+tc.key)
		if !strings.Contains(body, tc.kind) || !strings.Contains(body, "<dd>"+tc.image) {
			t.Errorf("%s lacks profile details", tc.key)
		}
		if tc.key == "install_72" && (!strings.Contains(body, "<dd>storage</dd>") || !strings.Contains(body, "<dd>Lab")) {
			t.Error("Windows installation options missing from inspector")
		}
	}
}

func TestUnifiedWindowsDriverPublicationKeepsChecks(t *testing.T) {
	s, store, published := unifiedMenuFixture(t)
	store.installations[1].DriverConfigID = 2
	store.draft.Items = []MenuDraftItem{{InstallationID: 72, Title: "Laptop"}}
	s.DriverConfigs, s.DriverPackages = &recordingDriverCheck{}, &recordingPackageCheck{}
	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther || len(*published) != 1 {
		t.Fatalf("driver publish=%d %s", res.Code, res.Body.String())
	}
	for _, want := range []string{"kernel " + testBootURL + "/assets/windows/wimboot", "/assets/windows-driver-wims/" + strings.Repeat("1", 64) + "/boot.wim", "/assets/windows-driver-configs/2/omb-setup.cmd", "/assets/windows-answers/31/" + store.answers[0].SHA256 + "/omb-answer.xml"} {
		if !strings.Contains((*published)[0], want) {
			t.Errorf("missing %s", want)
		}
	}
	store.configs[1].Build.WIMSHA256 = strings.Repeat("a", 64)
	res = menuPost(t, s, "/menu/rollback", url.Values{"id": {"100"}})
	if res.Code < 400 || len(*published) != 1 {
		t.Fatalf("restored changed driver build: %d publishes=%d", res.Code, len(*published))
	}
}

func TestUnifiedMenuReportsChoiceBeforeBootFiles(t *testing.T) {
	for _, windows := range []string{"manual", "answer", "drivers"} {
		t.Run(windows, func(t *testing.T) {
			s, store, published := unifiedMenuFixture(t)
			switch windows {
			case "answer":
				store.installations[1].AnswerID = 31
			case "drivers":
				store.installations[1].DriverConfigID = 2
				s.DriverConfigs, s.DriverPackages = &recordingDriverCheck{}, &recordingPackageCheck{}
			}
			res := menuPost(t, s, "/menu/publish", url.Values{})
			if res.Code != http.StatusSeeOther || len(*published) != 1 {
				t.Fatalf("publish=%d", res.Code)
			}
			for _, id := range []string{"71", "72", "73"} {
				report := ":install_" + id + "\nimgfetch --name omb-choice " + testBootURL + "/boot/choice?id=" + id + " && imgfree omb-choice || echo -n\nkernel "
				if strings.Count((*published)[0], report) != 1 {
					t.Errorf("installation %s does not report exactly one choice before kernel with best-effort request", id)
				}
			}
		})
	}
}

func TestUnifiedMenuPublishesAndRollsBackSplitWindowsWIM(t *testing.T) {
	s, store, published := unifiedMenuFixture(t)
	s.DriverConfigs, s.DriverPackages = &recordingDriverCheck{}, &recordingPackageCheck{}
	bootSHA := strings.Repeat("b", 64)
	driverWIMKey := strings.Repeat("1", 64)
	store.bootImages = []WindowsBootImage{
		{
			ID: 10, SourceKind: WIMSourceKindFile, StorageID: 7, Path: "winpe/win11-boot.wim",
			Title: "Win11 Universal Boot", SHA256: bootSHA, HasSetupExe: true, State: "ready",
			WIMInfo: agent.WindowsWIMInfo{Known: true, Images: []agent.WindowsWIMImage{{Index: 2, Architecture: "amd64", Build: 26100}}},
		},
	}
	store.installImages = []WindowsInstallImage{
		{
			ID: 20, StorageID: 7, Path: "wims/win11-corp.wim", InstallFile: "install.wim",
			Title: "Win11 Corp Gold", State: "ready",
			WIMInfo: agent.WindowsWIMInfo{Known: true, Images: []agent.WindowsWIMImage{{Index: 1, Name: "Windows 11 Pro", EditionID: "Professional", Architecture: "amd64", Build: 26100}}},
		},
	}
	store.bootProfiles = append(store.bootProfiles, BootProfile{
		ID: 3, Kind: BootProfileWindowsWIM, InstallImageID: 20, Title: "Win11 Corp Gold",
	})
	// Add a driver config for standalone windows_wim (ProfileID=0).
	store.configs = append(store.configs, DriverConfig{
		ID: 40, ProfileID: 0, AnswerID: 0, Title: "Bare Drivers", State: "ready",
		PackageINFs: []agent.SelectedINF{{PackageSHA256: strings.Repeat("a", 64), INFPath: "NetKVM/netkvm.inf"}},
		Build: agent.DriverBuildResult{
			WIMKey: driverWIMKey, WIMSHA256: strings.Repeat("2", 64), ScriptSHA256: strings.Repeat("3", 64),
		},
	})
	// Item 72: ISO + swapped standalone boot.wim (no drivers) -> /assets/windows-boot-images/10/boot.wim
	store.installations[1].BootImageID = 10
	// Item 74: standalone windows_wim + boot.wim 10 + driver config 40 -> /assets/windows-driver-wims/<key>/boot.wim
	inst74 := Installation{ID: 74, BootProfileID: 3, Title: "Win11 Bare Drivers", Folder: "Windows", BootImageID: 10, InstallImageID: 20, DriverConfigID: 40}
	store.installations = append(store.installations, inst74)
	store.draft.Items = []MenuDraftItem{
		{InstallationID: 72, Title: "Windows ISO Swap", Folder: "Windows", Components: installationComponents(store.installations[1])},
		{InstallationID: 74, Title: "Win11 Bare Drivers", Folder: "Windows", Components: installationComponents(inst74)},
	}

	// Publish v1.
	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther || len(*published) != 1 {
		t.Fatalf("publish v1=%d body=%s", res.Code, res.Body.String())
	}
	v1Script := (*published)[0]
	for _, want := range []string{
		menuWIMMarker,
		"initrd " + testBootURL + "/assets/windows-boot-images/10/boot.wim boot.wim || goto install_72_failed\n",
		"initrd " + testBootURL + "/assets/windows-driver-wims/" + driverWIMKey + "/boot.wim boot.wim || goto install_74_failed\n",
		"/assets/windows-driver-configs/40/omb-setup.cmd",
	} {
		if !strings.Contains(v1Script, want) {
			t.Errorf("v1 menu.ipxe missing %q\nscript:\n%s", want, v1Script)
		}
	}
	v1ID := store.versions[0].ID

	// Publish v2 with only item 72 (without boot_image_id override).
	store.installations[1].BootImageID = 0
	store.draft.Items = []MenuDraftItem{
		{InstallationID: 72, Title: "Windows ISO Plain", Folder: "Windows", Components: installationComponents(store.installations[1])},
	}
	res = menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther || len(*published) != 2 {
		t.Fatalf("publish v2=%d body=%s", res.Code, res.Body.String())
	}

	// Rollback to v1 must succeed and restore v1 script & components.
	res = menuPost(t, s, "/menu/rollback", url.Values{"id": {fmt.Sprintf("%d", v1ID)}})
	if res.Code != http.StatusSeeOther || len(*published) != 3 {
		t.Fatalf("rollback=%d body=%s", res.Code, res.Body.String())
	}
	if (*published)[2] != v1Script {
		t.Fatalf("rollback did not republish v1 script")
	}
	if len(store.draft.Items) != 2 || store.draft.Items[0].Components == nil || store.draft.Items[0].Components.BootImageID != 10 || store.draft.Items[1].Components.InstallImageID != 20 {
		t.Fatalf("rollback did not restore split WIM components in draft: %+v", store.draft.Items)
	}
}

func TestUnifiedMenuISOBootSwapWithDriversAndSwappedInstallAnswerCompat(t *testing.T) {
	s, store, published := unifiedMenuFixture(t)
	var checkedReqs []agent.DriverBuildRequest
	s.DriverConfigs = &capturingDriverCheck{onCheck: func(check agent.DriverBuildCheck) {
		checkedReqs = append(checkedReqs, check.Request)
	}}
	s.DriverPackages = &recordingPackageCheck{}

	// Base ISO 17 has only Windows 11 Home (Core).
	store.profiles[0].WIMInfo = agent.WindowsWIMInfo{
		Known:  true,
		Images: []agent.WindowsWIMImage{{Index: 1, Name: "Windows 11 Home", EditionID: "Core", Architecture: "amd64", Build: 26100}},
	}
	// Second ISO 18 provides source_kind='iso' boot.wim.
	store.profiles = append(store.profiles, WindowsProfile{
		ID: 18,
		WindowsProfileSource: WindowsProfileSource{
			StorageID: 7, StorageRoot: "/mnt/omb-smb/key7", Path: "win11-boot-iso.iso",
			Size: 12, Modified: store.profiles[0].Modified, Title: "Win11 Boot ISO",
		},
		State: "ready", InstallFile: "install.wim",
	})
	s.Images.(*fakeImagesAPI).catalog.Images = append(s.Images.(*fakeImagesAPI).catalog.Images, images.Image{
		StorageID: 7, Path: "win11-boot-iso.iso", Size: 12, Modified: store.profiles[0].Modified, Present: true,
	})

	store.bootImages = []WindowsBootImage{
		{
			ID: 12, SourceKind: WIMSourceKindISO, WindowsProfileID: 18,
			Title: "boot.wim · Win11 Boot ISO", SHA256: strings.Repeat("d", 64), HasSetupExe: true, State: "ready",
			WIMInfo: agent.WindowsWIMInfo{Known: true, Images: []agent.WindowsWIMImage{{Index: 2, Architecture: "amd64", Build: 26100}}},
		},
	}
	// Swapped install.wim 20 has Windows 11 Pro (Professional).
	store.installImages = []WindowsInstallImage{
		{
			ID: 20, StorageID: 7, Path: "wims/win11-pro.wim", InstallFile: "install.wim",
			Title: "Win11 Pro Gold", State: "ready",
			WIMInfo: agent.WindowsWIMInfo{Known: true, Images: []agent.WindowsWIMImage{{Index: 1, Name: "Windows 11 Pro", EditionID: "Professional", Architecture: "amd64", Build: 26100}}},
		},
	}
	// Answer 31 targets Pro (incompatible with base ISO 17 Home, compatible with swapped install.wim 20 Pro).
	store.answers[0].Target = WindowsAnswerTarget{
		ImageMode:     "edition",
		EditionID:     "pro",
		EditionLabel:  "Windows 11 Pro",
		Architectures: []string{"amd64"},
	}

	// Item 72: ISO 17 (Home) + swapped install.wim 20 (Pro) + answer 31 (Pro), no drivers -> must publish cleanly
	// and point omb-setup.cmd to /assets/windows-install-images/20/omb-answer-setup-iso-17.cmd.
	store.installations[1].InstallImageID = 20
	store.installations[1].AnswerID = 31

	// Item 75: ISO 17 + source_kind='iso' boot.wim 12 (from ISO 18) + driver config 2.
	inst75 := Installation{ID: 75, BootProfileID: 2, Title: "ISO Swap Boot Drivers", Folder: "Windows", BootImageID: 12, InstallImageID: 20, DriverConfigID: 2}
	store.installations = append(store.installations, inst75)
	store.draft.Items = []MenuDraftItem{
		{InstallationID: 72, Title: "ISO Home Swapped Pro", Folder: "Windows", Components: installationComponents(store.installations[1])},
		{InstallationID: 75, Title: "ISO Swap Boot Drivers", Folder: "Windows", Components: installationComponents(inst75)},
	}

	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther || len(*published) != 1 {
		t.Fatalf("publish=%d body=%s", res.Code, res.Body.String())
	}
	script := (*published)[0]
	if !strings.Contains(script, "/assets/windows-install-images/20/omb-answer-setup-iso-17.cmd") {
		t.Fatalf("expected ISO-swapped answer startup script URL, got:\n%s", script)
	}
	if len(checkedReqs) != 1 || checkedReqs[0].BootImageID != 0 || checkedReqs[0].BootProfileID != 18 || checkedReqs[0].ProfileID != 17 {
		t.Fatalf("expected CheckDriverConfig with BootImageID=0 BootProfileID=18 ProfileID=17, got %+v", checkedReqs)
	}

	// Rollback must also pass CheckDriverConfig with BootProfileID=18.
	v1ID := store.versions[0].ID
	checkedReqs = nil
	res = menuPost(t, s, "/menu/rollback", url.Values{"id": {fmt.Sprintf("%d", v1ID)}})
	if res.Code != http.StatusSeeOther || len(*published) != 2 {
		t.Fatalf("rollback=%d body=%s", res.Code, res.Body.String())
	}
	if len(checkedReqs) != 1 || checkedReqs[0].BootImageID != 0 || checkedReqs[0].BootProfileID != 18 {
		t.Fatalf("rollback CheckDriverConfig mismatch: %+v", checkedReqs)
	}
}

type capturingDriverCheck struct {
	onCheck func(agent.DriverBuildCheck)
}

func (c *capturingDriverCheck) PrepareDriverConfig(context.Context, agent.DriverBuildRequest) (agent.DriverBuildResult, error) {
	return agent.DriverBuildResult{}, nil
}
func (c *capturingDriverCheck) CheckDriverConfig(_ context.Context, check agent.DriverBuildCheck) error {
	if c.onCheck != nil {
		c.onCheck(check)
	}
	return nil
}
