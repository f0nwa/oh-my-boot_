package control

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

type unifiedMenuStore struct {
	*draftStore
	installations []Installation
	bootProfiles  []BootProfile
}

func (s *unifiedMenuStore) Installations(context.Context) ([]Installation, error) {
	return s.installations, nil
}
func (s *unifiedMenuStore) BootProfiles(context.Context) ([]BootProfile, error) {
	return s.bootProfiles, nil
}

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
		"kernel " + testBootURL + "/assets/ubuntu/vmlinuz initrd=initrd boot=casper ip=dhcp iso-url=" + testBootURL + "/images/ubuntu-desktop.iso ---\n",
		"initrd " + testBootURL + "/assets/ubuntu/initrd\n",
		"iseq ${platform} efi && goto install_73_efi || goto install_73_boot\n",
		"shim " + testBootURL + "/assets/ubuntu/bootx64.efi || goto install_73_failed\n",
		"kernel " + testBootURL + "/assets/windows/wimboot\n",
		"initrd " + testBootURL + "/assets/windows-profiles/17/boot.wim boot.wim\n",
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
	if !strings.Contains((*published)[0], "/assets/windows-answers/31/pxe-answer.xml") || !strings.Contains((*published)[0], "goto install_72_failed") {
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
		if !strings.Contains(body, tc.kind) || !strings.Contains(body, "<dd>"+tc.image+"</dd>") {
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
	for _, want := range []string{"kernel " + testBootURL + "/assets/windows/wimboot", "/assets/windows-driver-wims/" + strings.Repeat("1", 64) + "/boot.wim", "/assets/windows-driver-configs/2/pxe-setup.cmd", "/assets/windows-answers/31/pxe-answer.xml"} {
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
				report := ":install_" + id + "\nimgfetch --name pxe-choice " + testBootURL + "/boot/choice?id=" + id + " && imgfree pxe-choice || echo -n\nkernel "
				if strings.Count((*published)[0], report) != 1 {
					t.Errorf("installation %s does not report exactly one choice before kernel with best-effort request", id)
				}
			}
		})
	}
}
