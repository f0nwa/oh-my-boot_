package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type menuPageStore struct {
	authOnlyStore
	versions []MenuVersion
}

func (s menuPageStore) MenuVersions(context.Context) ([]MenuVersion, error) {
	return s.versions, nil
}

func folderTestProfiles() []WindowsProfile {
	return []WindowsProfile{
		{ID: 1, WindowsProfileSource: WindowsProfileSource{Title: "Windows 10x64", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"},
		{ID: 2, WindowsProfileSource: WindowsProfileSource{Title: "Windows 11 x64", Folder: "Windows"}, State: "ready", InstallFile: "install.wim"},
	}
}

func TestMenuFoldersProduceNestedNavigationAndBootEntries(t *testing.T) {
	form := url.Values{
		"title": {"oh my boot_"}, "label": {"Diagnostics"}, "message": {"Ready"},
		"install_999": {"on"}, "install_1001": {"on"}, "install_1002": {"on"},
		"folder_install_999":  {"Linux"},
		"folder_install_1001": {"Windows/Installers"},
		"folder_install_1002": {"Windows/Installers"},
	}
	script, err := buildTestMenuWithProfiles(form, "http://10.30.0.10:8080", folderTestProfiles())
	if err != nil {
		t.Fatal(err)
	}
	got := string(script)
	for _, want := range []string{
		"menu oh my boot_" + menuTitleSuffix + "\n",
		"menu oh my boot_ / Linux" + menuTitleSuffix + "\n",
		"menu oh my boot_ / Windows" + menuTitleSuffix + "\n",
		"menu oh my boot_ / Windows / Installers" + menuTitleSuffix + "\n",
		"item install_999 Ubuntu Desktop 26.04.1\n",
		"iseq ${platform} efi && goto install_999_efi || goto install_999_boot\n" +
			":install_999_efi\n" +
			"shim http://10.30.0.10:8080/assets/ubuntu/bootx64.efi || goto install_999_failed\n" +
			":install_999_boot\n" +
			"boot || goto install_999_failed\n",
		"item install_1001 Windows 10x64\n",
		"item install_1002 Windows 11 x64\n",
		"kernel http://10.30.0.10:8080/assets/windows/wimboot\n",
		"initrd -n winpeshl.ini http://10.30.0.10:8080/assets/windows-profiles/2/winpeshl.ini\n",
		"initrd -n pxe-setup.cmd http://10.30.0.10:8080/assets/windows-profiles/2/pxe-setup.cmd\n",
		"initrd http://10.30.0.10:8080/assets/windows-profiles/2/boot.wim boot.wim\n",
		"item local Boot local disk\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in generated menu", want)
		}
	}
	if strings.Count(got, "menu oh my boot_ / Windows / Installers"+menuTitleSuffix+"\n") != 1 {
		t.Fatal("shared folder was emitted more than once")
	}
	if strings.Index(got, "item install_1001 ") > strings.Index(got, "item install_1002 ") {
		t.Fatal("profile order changed in shared folder")
	}
	if strings.Count(got, ".. Back\n") != 3 {
		t.Fatalf("expected one Back item per folder, got script:\n%s", got)
	}
	for _, want := range []string{
		"item folder_1 " + menuItemText("Linux", ">") + "\nitem folder_2 " + menuItemText("Windows", ">") + "\nitem --gap\nitem local Boot local disk\nitem diagnostics Diagnostics\n",
		":folder_2\nmenu oh my boot_ / Windows" + menuTitleSuffix + "\nitem menu_root .. Back\nitem --gap\nitem folder_3 " + menuItemText("Installers", ">") + "\n",
		":folder_3\nmenu oh my boot_ / Windows / Installers" + menuTitleSuffix + "\nitem folder_2 .. Back\nitem --gap\nitem install_1001 Windows 10x64\nitem install_1002 Windows 11 x64\n",
		"choose target || goto folder_2\n",
		"choose --default local --timeout 10000 target || goto local\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("navigation section missing %q", want)
		}
	}
}

func TestMenuFoldersRejectUnsafeOrMalformedPaths(t *testing.T) {
	for _, folder := range []string{"Linux//Tools", "/Linux", "Linux/", "Linux\nitem evil", "Linux/${target}", "A/B/C/D/E/F"} {
		form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_999": {"on"}, "folder_install_999": {folder}}
		if _, err := buildTestMenuFromForm(form, "http://10.30.0.10:8080"); err == nil {
			t.Errorf("accepted Ubuntu folder %q", folder)
		}
		form = url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "install_1002": {"on"}, "folder_install_1002": {folder}}
		if _, err := buildTestMenuWithProfiles(form, "http://10.30.0.10:8080", folderTestProfiles()); err == nil {
			t.Errorf("accepted profile folder %q", folder)
		}
	}
}

func TestMenuFoldersRoundTripAndIgnoreUnselectedProfile(t *testing.T) {
	form := url.Values{
		"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"},
		"install_999": {"on"}, "folder_install_999": {"Linux/Live"},
		"install_1001": {"on"}, "folder_install_1001": {"Windows"},
		"folder_install_1002": {"Windows/Unused"},
	}
	script, err := buildTestMenuWithProfiles(form, "http://10.30.0.10:8080", folderTestProfiles())
	if err != nil {
		t.Fatal(err)
	}
	folders := menuFolderPaths(string(script))
	if folders["install_999"] != "Linux/Live" || folders["install_1001"] != "Windows" || folders["install_1002"] != "" {
		t.Fatalf("wrong stored folder choices: %#v", folders)
	}
	if strings.Contains(string(script), "item install_1002 ") {
		t.Fatal("unselected profile entered the menu")
	}
	if got := menuFolderPaths("#!ipxe\nmenu PXE\n"); len(got) != 0 {
		t.Fatalf("legacy menu should have no folder choices: %#v", got)
	}
}

func TestMenuFoldersPagePrefillsActiveFolder(t *testing.T) {
	server, store, _ := unifiedMenuFixture(t)
	store.versions = []MenuVersion{{Active: true, Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuVersionItem{{InstallationID: 71, Title: "Ubuntu Lab", Folder: "Linux/Live"}}}}
	store.draft = nil
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, accountRequest(http.MethodGet, "/menu?item=install_71", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `name="folder" value="Linux/Live"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
