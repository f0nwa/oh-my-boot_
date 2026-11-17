package control

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"omb/internal/drivers"
)

type driverPageStore struct {
	authOnlyStore
	packages []DriverPackage
}

func (s *driverPageStore) CreateDriverPackage(_ context.Context, inventory drivers.Inventory) (bool, error) {
	for _, existing := range s.packages {
		if existing.SHA256 == inventory.SHA256 {
			return false, nil
		}
	}
	s.packages = append(s.packages, DriverPackage{SHA256: inventory.SHA256, Inventory: inventory})
	return true, nil
}
func (s *driverPageStore) DriverPackages(context.Context) ([]DriverPackage, error) {
	return s.packages, nil
}
func (s *driverPageStore) DriverConfigs(context.Context) ([]DriverConfig, error) {
	return []DriverConfig{}, nil
}

type fakeDriverAgent struct{ imports []drivers.Inventory }

func (a *fakeDriverAgent) ImportDriverPackage(_ context.Context, body io.Reader, size int64) (drivers.Inventory, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return drivers.Inventory{}, err
	}
	if int64(len(data)) != size {
		return drivers.Inventory{}, fmt.Errorf("length differs")
	}
	inventory, err := drivers.InspectZIP(bytes.NewReader(data), size)
	if err == nil {
		a.imports = append(a.imports, inventory)
	}
	return inventory, err
}
func (a *fakeDriverAgent) CheckDriverPackage(context.Context, string) error { return nil }

func testDriverUpload(t *testing.T, kind, csrf, filename string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("csrf", csrf); err != nil {
		t.Fatal(err)
	}
	if kind == "folder" {
		if err := w.WriteField("paths[]", filename); err != nil {
			t.Fatal(err)
		}
		filename = "driver.inf"
		kind = "files[]"
	}
	f, err := w.CreateFormFile(kind, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/drivers/import", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	return req
}

func oneDriverZIP(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	w := zip.NewWriter(&body)
	f, err := w.Create("vendor/driver.inf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("[Version]\nClass=Net\n[Models.NTamd64]\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func TestDriverImportFolderMatchesZIP(t *testing.T) {
	store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}}
	api := &fakeDriverAgent{}
	s := &Server{Store: store, DriverPackages: api}
	for _, input := range []struct {
		kind, name string
		data       []byte
	}{
		{"zip", "driver.zip", oneDriverZIP(t)},
		{"folder", "vendor/driver.inf", []byte("[Version]\nClass=Net\n[Models.NTamd64]\n")},
	} {
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, testDriverUpload(t, input.kind, CSRFToken("test-token"), input.name, input.data))
		if res.Code != http.StatusSeeOther {
			t.Fatalf("%s: %d %s", input.kind, res.Code, res.Body.String())
		}
	}
	if len(api.imports) != 2 || api.imports[0].SHA256 != api.imports[1].SHA256 {
		t.Fatalf("folder and ZIP differ: %+v", api.imports)
	}
}

func TestDriverImportRejectsCSRFAndTraversal(t *testing.T) {
	for _, tc := range []struct {
		name, csrf, path string
		want             int
	}{
		{"csrf", "wrong", "vendor/driver.inf", http.StatusForbidden},
		{"traversal", CSRFToken("test-token"), "../driver.inf", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}}
			api := &fakeDriverAgent{}
			res := httptest.NewRecorder()
			(&Server{Store: store, DriverPackages: api}).Handler().ServeHTTP(res, testDriverUpload(t, "folder", tc.csrf, tc.path, []byte("[Version]\n")))
			if res.Code != tc.want || len(api.imports) != 0 {
				t.Fatalf("status=%d imports=%d", res.Code, len(api.imports))
			}
		})
	}
}

func TestDriversPageObserverIsReadOnly(t *testing.T) {
	store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
	s := &Server{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/drivers", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), `action="/drivers/import"`) {
		t.Fatalf("observer page: %d %s", res.Code, res.Body.String())
	}
}

func TestDriverImportRejectsObserver(t *testing.T) {
	store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
	api := &fakeDriverAgent{}
	res := httptest.NewRecorder()
	(&Server{Store: store, DriverPackages: api}).Handler().ServeHTTP(res, testDriverUpload(t, "zip", CSRFToken("test-token"), "driver.zip", oneDriverZIP(t)))
	if res.Code != http.StatusForbidden || len(api.imports) != 0 {
		t.Fatalf("observer upload: %d", res.Code)
	}
}

func TestDriversPageOperatorShowsBothImportMethods(t *testing.T) {
	store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}}
	req := httptest.NewRequest(http.MethodGet, "/drivers", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	(&Server{Store: store}).Handler().ServeHTTP(res, req)
	for _, required := range []string{`webkitdirectory`, `name="zip"`, `name="csrf"`, `/assets/driver-upload.js`} {
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), required) {
			t.Fatalf("missing %q in page: %d", required, res.Code)
		}
	}
}

func TestDriverImportReportsExistingPackage(t *testing.T) {
	store := &driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}}
	s := &Server{Store: store, DriverPackages: &fakeDriverAgent{}}
	var messages []string
	for i := 0; i < 2; i++ {
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, testDriverUpload(t, "zip", CSRFToken("test-token"), "driver.zip", oneDriverZIP(t)))
		location, _ := url.QueryUnescape(res.Header().Get("Location"))
		if res.Code != http.StatusSeeOther {
			t.Fatalf("import %d: %d %s", i, res.Code, res.Body.String())
		}
		messages = append(messages, location)
	}
	if messages[0] != "/drivers?message=Пакет драйвера добавлен" || messages[1] != "/drivers?message=Пакет уже есть в каталоге" || len(store.packages) != 1 {
		t.Fatalf("messages=%q packages=%d", messages, len(store.packages))
	}
}

func TestFilterAndSortDriverConfigs(t *testing.T) {
	configs := []DriverConfig{
		{ID: 1, Title: "Dell Precision 3581", Author: "vlad", SizeBytes: 500000000},
		{ID: 2, Title: "Lenovo ThinkPad P16", Author: "admin", SizeBytes: 400000000},
		{ID: 3, Title: "HP EliteBook 840", Author: "alex", SizeBytes: 600000000},
	}
	usage := driverUsage{
		Configs: map[int64][]driverUse{
			1: {{ID: 10, Title: "Install 1"}},
			2: {{ID: 11, Title: "Install 2"}, {ID: 12, Title: "Install 3"}},
		},
	}

	// Filter by title
	f1 := filterDriverConfigs(configs, usage, "dell")
	if len(f1) != 1 || f1[0].ID != 1 {
		t.Fatalf("unexpected filter by dell: %+v", f1)
	}

	// Sort config asc
	s1 := append([]DriverConfig(nil), configs...)
	sortDriverConfigs(s1, usage, "config", "asc")
	if s1[0].Title != "Dell Precision 3581" || s1[1].Title != "HP EliteBook 840" || s1[2].Title != "Lenovo ThinkPad P16" {
		t.Fatalf("unexpected sort config asc: %+v", s1)
	}

	// Sort size desc
	s2 := append([]DriverConfig(nil), configs...)
	sortDriverConfigs(s2, usage, "size", "desc")
	if s2[0].SizeBytes != 600000000 || s2[1].SizeBytes != 500000000 || s2[2].SizeBytes != 400000000 {
		t.Fatalf("unexpected sort size desc: %+v", s2)
	}

	// Sort uses desc
	s3 := append([]DriverConfig(nil), configs...)
	sortDriverConfigs(s3, usage, "uses", "desc")
	if s3[0].ID != 2 || s3[1].ID != 1 || s3[2].ID != 3 {
		t.Fatalf("unexpected sort uses desc: %+v", s3)
	}
}
