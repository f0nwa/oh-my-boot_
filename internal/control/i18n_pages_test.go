package control

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
	"omb/internal/drivers"
	"omb/internal/images"
	"omb/internal/network"
)

var cyrillic = regexp.MustCompile(`[\x{0400}-\x{04FF}]+`)

var wipesTrue, wipesFalse = true, false

func samplePageData(lang string) pageData {
	data := samplePageDataBase(lang)
	data.Preview = &menuPreview{
		Title: "oh my boot_ / Windows",
		Rows:  []previewRow{{Text: ".. Back", Href: "/menu?screen=menu_root"}, {Text: "Windows 11", Href: "/menu?item=install_1002", Selected: true}, {Text: "Ready", Href: "/menu?item=install_2005", Hint: "!! WIPES DISK", Danger: true}},
		Lines: []previewLine{{Text: "Diagnostics", Key: "diagnostics", Href: "/menu?item=diagnostics", Mark: "·", Fixed: true}, {Text: "Windows", Href: "/menu?screen=folder_1", Mark: "▾", Folder: true}, {Text: "Windows 11", Key: "install_1002", Parent: "folder_1", Href: "/menu?item=install_1002", Pad: "  ", Mark: "·", Selected: true, Movable: true},
			{Text: "Lab", Key: "install_2003", Parent: "folder_1", Href: "/menu?item=install_2003", Pad: "  ", Mark: "·", Added: true, Broken: true, ShowKey: true, Movable: true}},
		Removed: []string{"VirtIO"}, Key: "install_1002", Position: 1, Count: 2,
	}
	data.Preview.Item = &data.DraftRows[0]
	data.Preview.Install = &data.Installations[1]
	return data
}

func samplePageDataBase(lang string) pageData {
	now := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	cfg := network.Config{Mode: "proxy", Interface: "ens19", ServerIP: "10.30.0.10", Subnet: "10.30.0.0/24", DHCPStart: "10.30.0.100", DHCPEnd: "10.30.0.200", BootURL: "http://10.30.0.10:8080/boot/menu.ipxe", TFTPRoot: "/srv/omb/tftp"}
	profile := WindowsProfile{ID: 2, WindowsProfileSource: WindowsProfileSource{StorageID: 6, Path: "win11.iso", Title: "Windows 11", Folder: "Windows"}, State: "unavailable", Error: "Исходный ISO изменился или недоступен"}
	answer := WindowsAnswerVariant{ID: 3, Title: "Lab", Folder: "Windows/Tests", State: "error", Error: "Не удалось подготовить скрипт"}
	config := DriverConfig{ID: 4, ProfileID: 2, AnswerID: 3, Title: "VirtIO", Folder: "Windows/Tests", PackageINFs: []agent.SelectedINF{{PackageSHA256: "abc", INFPath: "netkvm.inf"}}, State: "error", Error: "Не удалось подготовить WinPE: недостаточно свободного места"}
	return pageData{
		Lang:       lang,
		Info:       requestContext{User: User{ID: 1, Username: "admin", Role: "admin", Language: lang, FSAccessMode: "rw", HasFSPassword: true, FSPasswordChangedAt: &now}, CSRF: "token", Path: "/menu", Release: ReleaseInfo{Version: "0.2.0~dev.1", DisplayVersion: "v0.2.0-dev.1", Channel: ChannelDev, Commit: "b7f1cabfee6b7defbd7b132d70de42c537c20530", Package: "omb 0.2.0~dev.1"}},
		FileServer: fileServerView{
			SMBEnabled:         true,
			SFTPEnabled:        true,
			SFTPPort:           2022,
			SMBAddress:         `\\10.30.0.10\iso`,
			SFTPAddress:        "sftp://10.30.0.10:2022/",
			HostKeyFingerprint: "SHA256:abc12345",
			ActiveUsersCount:   2,
			ROUsersCount:       1,
			RWUsersCount:       1,
		},
		CSRF:       "token",
		Message:    "Меню опубликовано",
		Error:      "Профиль Windows ISO недоступен: хранилище «DSM» недоступно: host is down",
		BootStatus: "Работает · abc", AgentStatus: "Недоступен", EventError: "Журнал временно недоступен", ImageError: "Сервис образов недоступен",
		Events:  []boot.Event{{Kind: "menu", Path: "/boot/menu.ipxe", RemoteAddr: "10.30.0.7:1", At: now}},
		Audit:   []AuditEvent{{At: now, Actor: "admin", Action: "menu.publish", Result: "ok", Detail: "sha256=x"}},
		Users:   []User{{ID: 1, Username: "admin", Role: "admin", Enabled: true, FSAccessMode: "rw", HasFSPassword: true, FSPasswordChangedAt: &now}, {ID: 2, Username: "viewer", Role: "observer", FSAccessMode: "ro", HasFSPassword: true, FSPasswordChangedAt: &now}, {ID: 3, Username: "guest", Role: "observer", FSAccessMode: "none"}},
		Network: cfg, OldNetwork: cfg, Confirmation: "confirm", HasNetwork: true,
		Versions:      []menuVersionRow{{MenuVersion: MenuVersion{ID: 2, At: now, Title: "PXE", Active: true}, Restorable: true}, {MenuVersion: MenuVersion{ID: 1, At: now, Title: "Old"}, Restorable: false}, {MenuVersion: MenuVersion{ID: 0, At: now, Title: "Older"}, Restorable: true}},
		Draft:         MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "ready", Items: []MenuDraftItem{{InstallationID: 1002}, {InstallationID: 2003, Folder: "Windows/Tests"}}},
		DraftRows:     []menuDraftRow{{Item: MenuDraftItem{InstallationID: 1002}, Title: "Windows 11", Kind: "Образ", Available: true}, {Item: MenuDraftItem{InstallationID: 2003}, Title: "Lab", Kind: "Образ и файл ответов", Error: "Windows answer 3 is not ready for PXE"}},
		Changes:       []MenuChange{{Kind: "add", Key: "install_2003", Title: "Lab"}, {Kind: "remove", Key: "install_3004", Title: "VirtIO"}, {Kind: "change", Key: "install_1002", Title: "Windows 11"}, {Kind: "order"}, {Kind: "settings"}},
		ActiveVersion: 2,
		Installations: []installationRow{{Key: "install_1002", Title: "Windows 11", Image: "Windows 11", Status: "in_menu"}, {Key: "install_2005", Title: "Ready", Image: "Ready", Answer: "Ready", AnswerWipesDisk: &wipesTrue, Status: "pending_add"}, {Key: "install_1003", Status: "pending_remove"}, {Key: "install_1004", Status: "not_in_menu"}, {Key: "install_3004", Drivers: "netkvm", Status: "needs_build", ConfigID: 4}, {Key: "install_3005", Status: "building"}, {Key: "install_3006", Status: "error", Error: "Не удалось подготовить WinPE"}, {Key: "install_1009", Status: "unavailable"}},
		Storages: []images.Storage{
			{ID: 6, Name: "DSM", Kind: "smb", Server: "nas", Share: "iso", State: "unavailable", Error: "Хранилище недоступно: не отвечает дольше 20s"},
			{ID: 7, Name: "Upload", Kind: "smb", Server: "nas", Share: "iso", State: "ready"},
			{ID: 8, Name: "NFS", Kind: "nfs", Server: "nas", ExportPath: "/srv/nfs", NFSVersion: "4.1", State: "ready"},
			{ID: 1, Name: "Local", Kind: "local", Root: "/srv/iso", State: "scanning"},
			{ID: 2, Name: "Mounted", Kind: "mounted", Root: "/mnt/iso", State: "other", Error: "Ожидает подключения"},
		},
		ISOImages:             []imageRow{{Image: images.Image{StorageID: 7, Path: "win10.iso", Size: 1, Present: true}, StorageName: "Upload"}, {Image: images.Image{StorageID: 7, Path: "arm64.iso", Present: false}, StorageName: "Upload", Problem: "ARM64 пока не поддерживается"}},
		WindowsProfiles:       []WindowsProfile{profile, {ID: 3, WindowsProfileSource: WindowsProfileSource{Title: "Ready"}, State: "ready"}},
		WindowsAnswerVariants: []WindowsAnswerVariant{answer, {ID: 5, Title: "Ready", State: "ready", WipesDisk: &wipesTrue}, {ID: 6, Title: "Safe", State: "ready", WipesDisk: &wipesFalse}},
		PackageChoices:        []packageChoice{{SHA256: "abc", Label: "netkvm", INFs: []infChoice{{Value: "abc:netkvm.inf", Label: "netkvm.inf · Net"}}}, {SHA256: "def", Label: "netkvm, vioscsi", INFs: []infChoice{{Value: "def:netkvm.inf", Label: "netkvm.inf · Net"}, {Value: "def:vioscsi.inf", Label: "vioscsi.inf · SCSIAdapter"}}}},
		DriverPackages:        []DriverPackage{{SHA256: "abc", Inventory: drivers.Inventory{Files: []drivers.File{{}}, INFs: []drivers.INF{{Path: "netkvm.inf", Architecture: "amd64", Class: "Net", HardwareIDs: []string{"PCI\\VEN_1AF4"}}}}}},
		DriverConfigs:         []DriverConfig{config},
	}
}

func TestEnglishPagesContainNoRussianText(t *testing.T) {
	for _, name := range []string{"login", "change_password", "installations", "installation_new", "account", "events", "network", "network_preview", "menu", "users", "audit", "images", "answers", "storages", "drivers", "machine", "machines", "system", "search"} {
		for _, data := range []pageData{samplePageData(langEN), {Lang: langEN, Info: requestContext{User: User{ID: 1, Role: "observer", Language: langEN}}}} {
			res := httptest.NewRecorder()
			render(res, name, data)
			body := strings.ReplaceAll(res.Body.String(), "Русский", "")
			if res.Code != http.StatusOK {
				t.Fatalf("%s: status %d: %s", name, res.Code, body)
			}
			if found := cyrillic.FindAllString(body, 5); len(found) > 0 {
				t.Errorf("%s (%s) has untranslated text %q", name, data.Info.User.Role, found)
			}
			if !strings.Contains(body, `<html lang="en" `) && name != "network_preview" {
				t.Errorf("%s is not marked as English", name)
			}
		}
	}
}

func TestRussianPagesStayRussian(t *testing.T) {
	res := httptest.NewRecorder()
	render(res, "menu", samplePageData(langRU))
	body := res.Body.String()
	for _, want := range []string{`<html lang="ru" `, "Меню загрузки", "Меню опубликовано", "хранилище «DSM» недоступно"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Russian page lacks %q", want)
		}
	}
}

// TestRussianSourceTextIsTranslated finds every Russian string literal in the
// package that can reach the browser and requires an English translation.
func TestRussianSourceTextIsTranslated(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "i18n.go" {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(fset, file, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if spec, ok := node.(*ast.ValueSpec); ok && len(spec.Names) == 1 && spec.Names[0].Name == "pagesTemplate" {
				return false
			}
			lit, ok := node.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil || !cyrillic.MatchString(text) {
				return true
			}
			if !russianLiteralTranslated(text) {
				t.Errorf("%s: no English translation for %q", fset.Position(lit.Pos()), text)
			}
			return true
		})
	}
}

func russianLiteralTranslated(text string) bool {
	if i := strings.Index(text, "?"); i >= 0 && strings.HasPrefix(text, "/") {
		query, err := url.ParseQuery(text[i+1:])
		if err != nil {
			return false
		}
		for _, values := range query {
			for _, value := range values {
				if cyrillic.MatchString(value) && translate(langEN, value) == value {
					return false
				}
			}
		}
		return true
	}
	if translate(langEN, text) != text {
		return true
	}
	// Literals joined with other text at run time, such as "Ошибка: " + err.
	for key := range englishMessages {
		if strings.HasPrefix(key, text+"%") || strings.HasSuffix(key, "%s"+text) || strings.HasPrefix(key, strings.TrimSuffix(text, " ")+" %") {
			return true
		}
	}
	return false
}

type languageStore struct {
	authOnlyStore
	saved string
}

func (s *languageStore) SetUserLanguage(_ context.Context, id int64, language string) error {
	s.saved = language
	return nil
}

func TestSetLanguageStoresChoiceAndReturnsSafely(t *testing.T) {
	for _, tc := range []struct{ language, ret, location string }{
		{"en", "/menu", "/menu"},
		{"ru", "/drivers", "/drivers"},
		{"en", "//evil.example/", "/"},
		{"en", "https://evil.example/", "/"},
		{"en", "/menu?x=1", "/"},
	} {
		store := &languageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
		form := url.Values{"csrf": {CSRFToken("test-token")}, "language": {tc.language}, "return": {tc.ret}}
		req := httptest.NewRequest(http.MethodPost, "/settings/language", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, req)
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != tc.location || store.saved != tc.language {
			t.Fatalf("%+v: status=%d location=%q saved=%q", tc, res.Code, res.Header().Get("Location"), store.saved)
		}
	}
	store := &languageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
	for _, form := range []url.Values{{"csrf": {CSRFToken("test-token")}, "language": {"de"}}, {"csrf": {"bad"}, "language": {"en"}}} {
		req := httptest.NewRequest(http.MethodPost, "/settings/language", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		res := httptest.NewRecorder()
		(&Server{Store: store}).Handler().ServeHTTP(res, req)
		if res.Code < 400 || store.saved != "" {
			t.Fatalf("bad language form accepted: %v status=%d saved=%q", form, res.Code, store.saved)
		}
	}
}

func TestEnglishUserGetsTranslatedPlainErrors(t *testing.T) {
	store := authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true, Language: langEN}}
	req := httptest.NewRequest(http.MethodPost, "/menu/publish", strings.NewReader("csrf="+CSRFToken("test-token")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	(&Server{Store: store}).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || strings.TrimSpace(res.Body.String()) != "Access denied" {
		t.Fatalf("denied response for English user: %d %q", res.Code, res.Body.String())
	}
}

func TestLoginPageFollowsBrowserLanguage(t *testing.T) {
	for _, tc := range []struct{ header, want string }{{"en-US,en;q=0.9", `lang="en"`}, {"ru-RU,ru;q=0.9,en;q=0.8", `lang="ru"`}, {"", `lang="ru"`}} {
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		req.Header.Set("Accept-Language", tc.header)
		res := httptest.NewRecorder()
		(&Server{}).Handler().ServeHTTP(res, req)
		if !bytes.Contains(res.Body.Bytes(), []byte(tc.want)) {
			t.Fatalf("Accept-Language %q: page lacks %s", tc.header, tc.want)
		}
	}
}

// TestScriptTextIsTranslated requires every Russian string shown by the upload
// scripts to have an English entry, or to be a redirect message the server translates.
func TestScriptTextIsTranslated(t *testing.T) {
	quoted := regexp.MustCompile(`'([^'\n]*[\x{0400}-\x{04FF}][^'\n]*)'`)
	for _, file := range []string{"iso_upload.js", "driver_upload.js", "password.js"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range quoted.FindAllStringSubmatch(string(src), -1) {
			text := match[1]
			key, _ := json.Marshal(text)
			if !bytes.Contains(src, append(key, ':')) && translate(langEN, text) == text {
				t.Errorf("%s: no English text for %q", file, text)
			}
		}
	}
}
