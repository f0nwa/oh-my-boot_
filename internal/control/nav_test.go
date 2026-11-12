package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func navPaths(groups []navGroup) map[string]bool {
	paths := map[string]bool{}
	for _, group := range groups {
		for _, item := range group.Items {
			paths[item.Path] = true
		}
	}
	return paths
}

func TestNavigationHidesItemsWithoutPermission(t *testing.T) {
	observer := navPaths(navigation("observer", "/"))
	if observer["/users"] || observer["/audit"] {
		t.Fatalf("observer sees admin-only items: %v", observer)
	}
	admin := navPaths(navigation("admin", "/"))
	if !admin["/users"] || !admin["/audit"] {
		t.Fatalf("admin misses items: %v", admin)
	}
}

func TestNavigationMarksActiveItem(t *testing.T) {
	for path, want := range map[string]string{"/images": "/images", "/": "/", "/ubuntu-autoinstall/new": "/answers", "/ubuntu-autoinstall/preview": "/answers", "/ubuntu-autoinstall/1": "/answers", "/ubuntu-autoinstall/1/copy": "/answers"} {
		var active []string
		for _, group := range navigation("admin", path) {
			for _, item := range group.Items {
				if item.Active {
					active = append(active, item.Path)
				}
			}
		}
		if len(active) != 1 || active[0] != want {
			t.Fatalf("path %s: active=%v", path, active)
		}
	}
}

func TestLayoutServesStylesheet(t *testing.T) {
	res := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/assets/style.css", nil))
	if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "text/css") || !strings.Contains(res.Body.String(), "--accent:#F2A93B") {
		t.Fatalf("response=%d %q", res.Code, res.Header().Get("Content-Type"))
	}
}

func TestContentSecurityPolicyAllowsOwnStylesheet(t *testing.T) {
	res := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/assets/style.css", nil))
	csp := res.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "style-src 'self'") {
		t.Fatalf("CSP blocks /assets/style.css: %q", csp)
	}
}

func TestNavigationGroupsLibraryAndSystemPages(t *testing.T) {
	groups := navigation("admin", "/drivers")
	var titles []string
	for _, group := range groups {
		titles = append(titles, group.Title)
	}
	if strings.Join(titles, ",") != "Сейчас,Меню,Библиотека,Журнал,Система" {
		t.Fatalf("sections=%v", titles)
	}
	tabs := subnav(groups)
	if len(tabs) != 5 || tabs[0].Path != "/installations" || tabs[1].Path != "/images" || !tabs[2].Active || tabs[2].Path != "/drivers" || tabs[3].Path != "/answers" || tabs[4].Path != "/usb" {
		t.Fatalf("library tabs=%+v", tabs)
	}
	if tabs := subnav(navigation("admin", "/installations/new")); len(tabs) != 5 || !tabs[0].Active {
		t.Fatalf("installation form must show library tabs: %+v", tabs)
	}
	for _, group := range navigation("admin", "/installations/new") {
		if group.Active != (group.Title == "Библиотека") {
			t.Fatalf("active section for /installations/new: %+v", group)
		}
	}
	observer := navigation("observer", "/account")
	system := observer[len(observer)-1]
	if system.Title != "Система" || system.Path != "/system" || len(system.Items) != 5 || system.Items[1].Path != "/machines" || !system.Active {
		t.Fatalf("observer system section=%+v", system)
	}
}

func TestFontsAreServedFromTheBinary(t *testing.T) {
	for name, want := range map[string]int{"Geist-Variable.woff2": http.StatusOK, "GeistMono-Variable.woff2": http.StatusOK, "OFL.txt": http.StatusNotFound, "..%2Fstyle.css": http.StatusNotFound} {
		res := httptest.NewRecorder()
		(&Server{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/assets/fonts/"+name, nil))
		if res.Code != want || (want == http.StatusOK && (res.Header().Get("Content-Type") != "font/woff2" || res.Body.Len() < 10000)) {
			t.Fatalf("%s: status=%d type=%q len=%d", name, res.Code, res.Header().Get("Content-Type"), res.Body.Len())
		}
	}
}

func TestStylesheetUsesEmbeddedFontsOnly(t *testing.T) {
	data, err := templateFS.ReadFile("templates/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)
	if !strings.Contains(css, "url(/assets/fonts/Geist-Variable.woff2)") || strings.Contains(css, "fonts.googleapis") || strings.Contains(css, "@import") {
		t.Fatal("stylesheet must load fonts only from /assets/fonts")
	}
}

func TestNowSectionIsNowInEnglish(t *testing.T) {
	if got := translate(langEN, "Сейчас"); got != "Now" {
		t.Fatalf("Сейчас → %q", got)
	}
}

func TestNavigationPlacesMenuBeforeLibrary(t *testing.T) {
	groups := navigation("admin", "/")
	var library, menu int = -1, -1
	for i, group := range groups {
		switch group.Title {
		case "Библиотека":
			library = i
			if group.Path != "/installations" {
				t.Fatalf("library must open boot entries: %+v", group)
			}
		case "Меню":
			menu = i
		}
	}
	if library < 0 || menu < 0 || menu >= library {
		t.Fatalf("unexpected navigation order: library=%d menu=%d", library, menu)
	}
}

func TestNavigationStaysVisibleWhenPageScrolls(t *testing.T) {
	data := samplePageDataBase(langRU)
	data.Info.Path = "/images"
	body := renderPage("images", data)
	if !strings.Contains(body, `<div class="nav-shell"><header class="topbar">`) || !strings.Contains(body, `</nav></div><main class="content">`) {
		t.Fatal("top navigation and section tabs are not grouped in a sticky shell")
	}
	css, err := templateFS.ReadFile("templates/style.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".nav-shell{position:sticky;top:0;z-index:") {
		t.Fatal("navigation shell does not stick to the top of the viewport")
	}
}
