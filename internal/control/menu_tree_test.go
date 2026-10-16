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

func TestMenuFoldersProduceNestedNavigationAndBootEntries(t *testing.T) {
	form := url.Values{
		"title": {"PXE Platform"}, "label": {"Diagnostics"}, "message": {"Ready"},
		"ubuntu": {"on"}, "windows10": {"on"}, "windows11": {"on"},
		"folder_ubuntu":    {"Linux"},
		"folder_windows10": {"Windows/Установщики"},
		"folder_windows11": {"Windows/Установщики"},
	}
	script, err := BuildMenuFromForm(form, "http://10.30.0.10:8080")
	if err != nil {
		t.Fatal(err)
	}
	got := string(script)
	for _, want := range []string{
		"menu PXE Platform\n",
		"menu PXE Platform / Linux\n",
		"menu PXE Platform / Windows\n",
		"menu PXE Platform / Windows / Установщики\n",
		"item ubuntu Ubuntu Desktop 26.04.1\n",
		"item windows10 Windows 10 installer - WinPE\n",
		"item windows11 Windows 11 installer - WinPE\n",
		"kernel http://10.30.0.10:8080/assets/windows/wimboot\n",
		"initrd -n winpeshl.ini http://10.30.0.10:8080/assets/windows11/winpeshl.ini\n",
		"initrd -n pxe-setup.cmd http://10.30.0.10:8080/assets/windows11/pxe-setup.cmd\n",
		"initrd http://10.30.0.10:8080/assets/windows11/boot.wim boot.wim\n",
		"item local Boot local disk\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in generated menu", want)
		}
	}
	if strings.Count(got, "menu PXE Platform / Windows / Установщики\n") != 1 {
		t.Fatal("shared folder was emitted more than once")
	}
	if strings.Index(got, "item windows10 ") > strings.Index(got, "item windows11 ") {
		t.Fatal("profile order changed in shared folder")
	}
	if strings.Count(got, ".. Back\n") != 3 {
		t.Fatalf("expected one Back item per folder, got script:\n%s", got)
	}
	for _, want := range []string{
		"item folder_1 Linux\nitem folder_2 Windows\nitem local Boot local disk\n",
		":folder_2\nmenu PXE Platform / Windows\nitem menu_root .. Back\nitem folder_3 Установщики\nchoose target || goto menu_root\n",
		":folder_3\nmenu PXE Platform / Windows / Установщики\nitem folder_2 .. Back\nitem windows10 Windows 10 installer - WinPE\nitem windows11 Windows 11 installer - WinPE\nchoose target || goto folder_2\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("navigation section missing %q", want)
		}
	}
}

func TestMenuFoldersRejectUnsafeOrMalformedPaths(t *testing.T) {
	for _, folder := range []string{"Windows//Tools", "/Windows", "Windows/", "Windows\nitem evil", "Windows/${target}", "A/B/C/D/E/F"} {
		form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "windows11": {"on"}, "folder_windows11": {folder}}
		if _, err := BuildMenuFromForm(form, "http://10.30.0.10:8080"); err == nil {
			t.Errorf("accepted folder %q", folder)
		}
	}
}

func TestMenuFoldersRoundTripAndIgnoreUnselectedProfile(t *testing.T) {
	form := url.Values{
		"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"},
		"ubuntu": {"on"}, "folder_ubuntu": {"Linux/Live"},
		"folder_windows10": {"Windows"},
	}
	script, err := BuildMenuFromForm(form, "http://10.30.0.10:8080")
	if err != nil {
		t.Fatal(err)
	}
	folders := menuFolderPaths(string(script))
	if folders["ubuntu"] != "Linux/Live" || folders["windows10"] != "" {
		t.Fatalf("wrong stored folder choices: %#v", folders)
	}
	if strings.Contains(string(script), "item windows10 ") {
		t.Fatal("unselected profile entered the menu")
	}
	if got := menuFolderPaths("#!ipxe\nmenu PXE\n"); len(got) != 0 {
		t.Fatalf("legacy menu should have no folder choices: %#v", got)
	}
}

func TestMenuFoldersPagePrefillsActiveFolder(t *testing.T) {
	form := url.Values{"title": {"PXE"}, "label": {"Diagnostics"}, "message": {"Ready"}, "windows11": {"on"}, "folder_windows11": {"Windows/Installers"}}
	script, err := BuildMenuFromForm(form, "http://10.30.0.10:8080")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: menuPageStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}},
		versions:      []MenuVersion{{Script: string(script), Active: true}},
	}}
	req := httptest.NewRequest(http.MethodGet, "/menu", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("menu page returned %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `name="folder_windows11" value="Windows/Installers"`) {
		t.Fatalf("active folder not shown in form: %s", res.Body.String())
	}
}
