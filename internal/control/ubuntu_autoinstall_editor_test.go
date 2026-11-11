package control

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

type autoinstallEditorStore struct {
	*newInstallStore
	files     []UbuntuAutoinstallFile
	audits    []string
	saveError bool
}

func (s *autoinstallEditorStore) UbuntuAutoinstalls(context.Context) ([]UbuntuAutoinstallSummary, error) {
	var out []UbuntuAutoinstallSummary
	for _, f := range s.files {
		out = append(out, UbuntuAutoinstallSummary{ID: f.ID, Title: f.Title, SHA256: f.SHA256, State: f.State, DiskMode: f.Values.DiskMode, DiskSerial: f.Values.DiskSerial, CreatedAt: f.CreatedAt})
	}
	return out, nil
}
func (s *autoinstallEditorStore) UbuntuAutoinstall(_ context.Context, id int64) (UbuntuAutoinstallFile, error) {
	for _, f := range s.files {
		if f.ID == id {
			return f, nil
		}
	}
	return UbuntuAutoinstallFile{}, errors.New("not found")
}
func (s *autoinstallEditorStore) CreateUbuntuAutoinstall(_ context.Context, f UbuntuAutoinstallFile) (UbuntuAutoinstallFile, error) {
	if s.saveError {
		return UbuntuAutoinstallFile{}, errors.New("database rejected private content: " + string(f.UserData))
	}
	f.ID = int64(len(s.files) + 1)
	f.CreatedAt = time.Now()
	s.files = append(s.files, f)
	return f, nil
}
func (s *autoinstallEditorStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, action+result+detail)
	return nil
}
func autoinstallServer(t *testing.T) (*Server, *autoinstallEditorStore) {
	t.Helper()
	server, base := newInstallServer(t)
	store := &autoinstallEditorStore{newInstallStore: base}
	server.Store = store
	return server, store
}

const constructorPassword = "dedicated-installation-secret"

func constructorForm() url.Values {
	return url.Values{"title": {"Lab Ubuntu"}, "locale": {"en_US.UTF-8"}, "keyboard": {"us"}, "timezone": {"Europe/Moscow"}, "hostname": {"lab-ubuntu"}, "username": {"installer"}, "password": {constructorPassword}, "disk_mode": {"interactive"}, "disk_serial": {""}}
}
func constructorPost(t *testing.T, s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, path, form))
	return res
}
func constructorPreview(t *testing.T, s *Server, form url.Values) (string, []byte) {
	t.Helper()
	res := constructorPost(t, s, "/ubuntu-autoinstall/preview", form)
	if res.Code != http.StatusOK {
		t.Fatalf("preview status=%d: %s", res.Code, res.Body.String())
	}
	tokenMatch := regexp.MustCompile(`name="preview_token" value="([^"]+)"`).FindStringSubmatch(res.Body.String())
	yamlMatch := regexp.MustCompile(`(?s)<pre id="autoinstall-yaml">(.*?)</pre>`).FindStringSubmatch(res.Body.String())
	if len(tokenMatch) != 2 || len(yamlMatch) != 2 {
		t.Fatal("preview missing token or exact YAML")
	}
	if strings.Contains(res.Body.String(), constructorPassword) {
		t.Fatal("preview leaked password")
	}
	return html.UnescapeString(tokenMatch[1]), []byte(html.UnescapeString(yamlMatch[1]))
}
func TestUbuntuConstructorPreviewSaveLibraryAndCopy(t *testing.T) {
	s, store := autoinstallServer(t)
	boot, published := fakeBoot(t)
	s.Boot = boot
	form := constructorForm()
	token, data := constructorPreview(t, s, form)
	form.Set("preview_token", token)
	res := constructorPost(t, s, "/ubuntu-autoinstall", form)
	if res.Code != http.StatusSeeOther || len(store.files) != 1 {
		t.Fatalf("save status=%d: %s", res.Code, res.Body.String())
	}
	f := store.files[0]
	if !bytes.Equal(f.UserData, data) || f.SHA256 != fmtDigest(data) {
		t.Fatal("save changed preview bytes")
	}
	encoded, _ := json.Marshal(f)
	if bytes.Contains(encoded, []byte(constructorPassword)) || strings.Contains(strings.Join(store.audits, " "), constructorPassword) {
		t.Fatal("plaintext persisted")
	}
	for _, path := range []string{"/answers", "/ubuntu-autoinstall/1"} {
		body := libraryGet(t, s, path)
		if !strings.Contains(body, "Lab Ubuntu") || strings.Contains(body, constructorPassword) {
			t.Fatalf("library/detail missing or unsafe: %s", path)
		}
	}
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/ubuntu-autoinstall/1/download", nil))
	if res.Code != 200 || !bytes.Equal(res.Body.Bytes(), data) || res.Header().Get("Cache-Control") != "no-store" || !strings.Contains(res.Header().Get("Content-Disposition"), "user-data") {
		t.Fatal("download changed preview bytes or headers")
	}
	copy := libraryGet(t, s, "/ubuntu-autoinstall/1/copy")
	if !strings.Contains(copy, `name="hostname" value="lab-ubuntu"`) || strings.Contains(copy, f.Values.PasswordHash) || strings.Contains(copy, constructorPassword) || strings.Contains(copy, `name="preview_token"`) {
		t.Fatal("copy did not prefill safe fields only")
	}
	form.Del("preview_token")
	form.Set("password", "")
	if constructorPost(t, s, "/ubuntu-autoinstall/preview", form).Code != 400 {
		t.Fatal("copy accepted empty new password")
	}
	if store.draft != nil || len(*published) != 0 {
		t.Fatal("library operation changed menu")
	}
}
func fmtDigest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func TestUbuntuConstructorReturnsToAnswerLibrary(t *testing.T) {
	for _, path := range []string{"/ubuntu-autoinstall/new", "/ubuntu-autoinstall/1", "/ubuntu-autoinstall/1/copy"} {
		t.Run(path, func(t *testing.T) {
			s, store := autoinstallServer(t)
			if path != "/ubuntu-autoinstall/new" {
				store.files = []UbuntuAutoinstallFile{{ID: 1, Title: "Lab Ubuntu", State: "validated"}}
			}
			body := libraryGet(t, s, path)
			crumb := regexp.MustCompile(`<p class="crumbs"><a href="([^"]+)"`).FindStringSubmatch(body)
			if len(crumb) != 2 {
				t.Fatal("constructor has no return link")
			}
			back, err := url.Parse(html.UnescapeString(crumb[1]))
			if err != nil {
				t.Fatal(err)
			}
			library := libraryGet(t, s, back.RequestURI())
			if !strings.Contains(library, `<h1>Файлы ответов</h1>`) || !strings.Contains(library, `href="/answers" class="active" aria-current="page"`) {
				t.Fatal("return link leaves the answer-file library and its navigation")
			}
			if back.Fragment == "" || !strings.Contains(library, `id="`+back.Fragment+`"`) {
				t.Fatal("return link does not target the Ubuntu section")
			}
			if !strings.Contains(library, `href="/ubuntu-autoinstall/new"`) {
				t.Fatal("returned library has no create action")
			}
		})
	}
}

func TestUbuntuConstructorPreviewDownload(t *testing.T) {
	s, store := autoinstallServer(t)
	token, data := constructorPreview(t, s, constructorForm())
	res := constructorPost(t, s, "/ubuntu-autoinstall/preview/download", url.Values{"preview_token": {token}})
	if res.Code != 200 || !bytes.Equal(data, res.Body.Bytes()) || res.Header().Get("Cache-Control") != "no-store" || !strings.Contains(res.Header().Get("Content-Disposition"), "user-data") {
		t.Fatalf("preview download status=%d", res.Code)
	}
	res = constructorPost(t, s, "/ubuntu-autoinstall/preview/download", url.Values{"preview_token": {token + "x"}})
	if res.Code != 400 || len(store.files) != 0 {
		t.Fatal("invalid preview download accepted or persisted")
	}
}

func TestUbuntuConstructorChangedFieldsAndInvalidTokens(t *testing.T) {
	s, store := autoinstallServer(t)
	token, _ := constructorPreview(t, s, constructorForm())
	for _, field := range []string{"title", "locale", "keyboard", "timezone", "hostname", "username", "disk_mode", "disk_serial", "password"} {
		t.Run(field, func(t *testing.T) {
			form := constructorForm()
			form.Set("preview_token", token)
			form.Set(field, "changed")
			res := constructorPost(t, s, "/ubuntu-autoinstall", form)
			if res.Code != 400 || strings.Contains(res.Body.String(), constructorPassword) {
				t.Fatalf("changed %s accepted or leaked", field)
			}
		})
	}
	for _, bad := range []string{"", token + "a", strings.Repeat("x", 13000)} {
		form := constructorForm()
		form.Set("preview_token", bad)
		if res := constructorPost(t, s, "/ubuntu-autoinstall", form); res.Code < 400 {
			t.Fatal("bad token accepted")
		}
	}
	// A valid signature from a different authenticated session must fail.
	form := constructorForm()
	form.Set("preview_token", token)
	form.Set("csrf", CSRFToken("other-session"))
	req := httptest.NewRequest("POST", "/ubuntu-autoinstall", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "other-session"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != 400 {
		t.Fatalf("cross-session token status=%d", res.Code)
	}
	// Change only expiry, then sign with the test session key to exercise expiry independently.
	parts := strings.Split(token, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	fields["expires"] = json.RawMessage("1")
	payload, _ = json.Marshal(fields)
	mac := hmac.New(sha256.New, []byte("test-token"))
	mac.Write(payload)
	form = constructorForm()
	form.Set("preview_token", base64.RawURLEncoding.EncodeToString(payload)+"."+base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
	if res := constructorPost(t, s, "/ubuntu-autoinstall", form); res.Code != 400 {
		t.Fatal("expired token accepted")
	}
	if len(store.files) != 0 || store.draft != nil {
		t.Fatal("failed saves mutated state")
	}
}
func TestUbuntuConstructorRandomSaltAndSerialWarning(t *testing.T) {
	s, _ := autoinstallServer(t)
	form := constructorForm()
	form.Set("disk_mode", "serial")
	form.Set("disk_serial", "LAB-DISK-123")
	token, a := constructorPreview(t, s, form)
	_, b := constructorPreview(t, s, form)
	if bytes.Equal(a, b) {
		t.Fatal("password salt reused")
	}
	decoded, _ := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[0])
	if bytes.Contains(decoded, []byte(constructorPassword)) {
		t.Fatal("token leaked password")
	}
	res := constructorPost(t, s, "/ubuntu-autoinstall/preview", form)
	for _, want := range []string{"LAB-DISK-123", "стирает диск"} {
		if !strings.Contains(res.Body.String(), want) {
			t.Fatalf("missing serial warning %s", want)
		}
	}
	form.Set("disk_serial", "*")
	res = constructorPost(t, s, "/ubuntu-autoinstall/preview", form)
	if res.Code != 400 || !strings.Contains(res.Body.String(), "disk serial") || strings.Contains(res.Body.String(), constructorPassword) {
		t.Fatal("invalid serial not reported safely")
	}
}
func TestUbuntuConstructorRoleCSRFAndPrivateErrors(t *testing.T) {
	for _, path := range []string{"/ubuntu-autoinstall/preview", "/ubuntu-autoinstall", "/ubuntu-autoinstall/preview/download"} {
		for _, kind := range []string{"observer", "csrf"} {
			t.Run(path+kind, func(t *testing.T) {
				s, store := autoinstallServer(t)
				req := accountRequest(http.MethodPost, path, constructorForm())
				if kind == "observer" {
					store.user.Role = "observer"
				} else {
					req = httptest.NewRequest("POST", path, strings.NewReader("csrf=bad"))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
				}
				res := httptest.NewRecorder()
				s.Handler().ServeHTTP(res, req)
				if res.Code != 403 || len(store.files) != 0 {
					t.Fatalf("status %d", res.Code)
				}
			})
		}
	}
	s, store := autoinstallServer(t)
	token, data := constructorPreview(t, s, constructorForm())
	form := constructorForm()
	form.Set("preview_token", token)
	store.saveError = true
	res := constructorPost(t, s, "/ubuntu-autoinstall", form)
	if res.Code != 503 || strings.Contains(res.Body.String(), constructorPassword) || strings.Contains(res.Body.String(), string(data)) || strings.Contains(strings.Join(store.audits, " "), "$y$") {
		t.Fatal("storage error leaked private content")
	}
}
func TestUbuntuConstructorInstallationBinding(t *testing.T) {
	s, store := autoinstallServer(t)
	boot, published := fakeBoot(t)
	s.Boot = boot
	file := testAutoinstallFile(t, "lab")
	file.ID = 1
	store.files = []UbuntuAutoinstallFile{file}
	for _, title := range []string{"Ubuntu Lab", "Ubuntu Recovery"} {
		res := constructorPost(t, s, "/installations", url.Values{"boot_profile_id": {"52"}, "title": {title}, "autoinstall_id": {"1"}})
		if res.Code != 303 {
			t.Fatalf("installation status=%d: %s", res.Code, res.Body.String())
		}
	}
	if len(store.installations) != 2 || store.installations[0].AutoinstallID != 1 || store.installations[1].AutoinstallID != 1 || len(*published) != 0 || store.draft != nil {
		t.Fatal("automation binding missing or published immediately")
	}
	for _, tc := range []struct{ profile, id string }{{"51", "1"}, {"52", "999"}, {"52", "bad"}, {"52", "-1"}} {
		if res := constructorPost(t, s, "/installations", url.Values{"boot_profile_id": {tc.profile}, "title": {"Bad"}, "autoinstall_id": {tc.id}}); res.Code != 400 {
			t.Fatalf("bad binding accepted %+v", tc)
		}
	}
	store.files[0].State = "invalid"
	if res := constructorPost(t, s, "/installations", url.Values{"boot_profile_id": {"52"}, "title": {"Bad"}, "autoinstall_id": {"1"}}); res.Code != 400 {
		t.Fatal("unvalidated file accepted")
	}
	store.files[0].State = "validated"
	ubuntu := libraryGet(t, s, "/installations/new?boot_profile_id=52")
	windows := libraryGet(t, s, "/installations/new?boot_profile_id=51")
	if !strings.Contains(ubuntu, `name="autoinstall_id"`) || strings.Contains(ubuntu, `id="ubuntu-autoinstall" hidden disabled`) || !strings.Contains(windows, `id="ubuntu-autoinstall" hidden disabled`) {
		t.Fatal("selector does not follow OS")
	}
	if body := libraryGet(t, s, "/installations"); !strings.Contains(body, file.Title) {
		t.Fatal("installation list omits automation")
	}
}
func TestUbuntuConstructorEnglishUI(t *testing.T) {
	s, store := autoinstallServer(t)
	store.user.Language = langEN
	for _, path := range []string{"/answers", "/ubuntu-autoinstall/new"} {
		body := libraryGet(t, s, path)
		if cyrillic.MatchString(strings.ReplaceAll(body, "Русский", "")) {
			t.Fatalf("untranslated UI %s", path)
		}
	}
	res := constructorPost(t, s, "/ubuntu-autoinstall/preview", constructorForm())
	if res.Code != 200 || cyrillic.MatchString(strings.ReplaceAll(res.Body.String(), "Русский", "")) {
		t.Fatal("preview untranslated")
	}
}

// Exercise the HTTP boundary through the real PostgreSQL store as well: only
// normalized values, the hash and the exact preview bytes may reach persistence.
func TestUbuntuConstructorPostgreSQL(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()
	var userID int64
	if err := store.Pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES('operator','unused-login-hash','operator') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')`, tokenHash("test-token"), userID); err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: store}
	form := constructorForm()
	form.Set("disk_mode", "default")
	token, data := constructorPreview(t, s, form)
	form.Set("preview_token", token)
	res := constructorPost(t, s, "/ubuntu-autoinstall", form)
	if res.Code != 303 {
		t.Fatalf("save status=%d: %s", res.Code, res.Body.String())
	}
	saved, err := store.UbuntuAutoinstall(ctx, 1)
	if err != nil || !bytes.Equal(saved.UserData, data) || saved.SHA256 != fmtDigest(data) {
		t.Fatal("database changed preview content")
	}
	var persisted string
	if err := store.Pool.QueryRow(ctx, `SELECT row_to_json(f)::text FROM ubuntu_autoinstall_files f WHERE id=1`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, constructorPassword) || !strings.Contains(persisted, "$y$") {
		t.Fatal("database persisted plaintext or omitted hash")
	}
	audits, err := store.AuditList(ctx)
	if err != nil || len(audits) != 1 || audits[0].Action != "ubuntu-autoinstall.create" || audits[0].Detail != "1" {
		t.Fatalf("audit action missing: %v", err)
	}
	encoded, _ := json.Marshal(audits)
	if bytes.Contains(encoded, []byte(constructorPassword)) || bytes.Contains(encoded, []byte("$y$")) {
		t.Fatal("database audit leaked account secret")
	}
	// A copy receives a new password and a fresh hash; the original stays exact.
	form = constructorForm()
	form.Set("password", "replacement-installation-password")
	token, _ = constructorPreview(t, s, form)
	form.Set("preview_token", token)
	if res := constructorPost(t, s, "/ubuntu-autoinstall", form); res.Code != 303 {
		t.Fatalf("copy save status=%d", res.Code)
	}
	again, err := store.UbuntuAutoinstall(ctx, 1)
	if err != nil || !bytes.Equal(again.UserData, data) {
		t.Fatal("copy mutated original")
	}
	copied, err := store.UbuntuAutoinstall(ctx, 2)
	if err != nil || copied.Values.PasswordHash == saved.Values.PasswordHash {
		t.Fatal("copy reused old account hash")
	}
	for _, table := range []string{"installations", "menu_draft", "menu_versions"} {
		var count int
		if err := store.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("constructor mutated %s", table)
		}
	}
}

func TestInstallationAutomationUsesRadioChoices(t *testing.T) {
	s, store := autoinstallServer(t)
	file := testAutoinstallFile(t, "lab")
	file.ID = 1
	store.files = []UbuntuAutoinstallFile{file}
	body := libraryGet(t, s, "/installations/new?boot_profile_id=52")
	for _, want := range []string{`name="autoinstall_id" value="0" checked`, `name="autoinstall_id" value="1"`, `id="ubuntu-step-no">2`, `id="menu-step-no">3`, `id="ubuntu-answer-search"`, `id="windows-answer-search"`} {
		if !strings.Contains(body, want) {
			t.Errorf("form lacks %q", want)
		}
	}
	if strings.Contains(body, `<select id="i-autoinstall"`) {
		t.Error("Ubuntu automation remains a select")
	}
}
