package control

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"omb/internal/agent"
	"omb/internal/drivers"
)

type driverExportAudit struct {
	action, result, detail string
}

type driverExportTestStore struct {
	driverPageStore
	audits []driverExportAudit
}

func (s *driverExportTestStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, driverExportAudit{
		action: action,
		result: result,
		detail: detail,
	})
	return nil
}

func buildControlTestZIP(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for k, v := range entries {
		f, err := zw.Create(k)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDriverPackageExportSingleSelectedAndAll(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mgr := &agent.DriverManager{Root: root}
	agentClient := inMemoryAgentClient(agent.HandlerWithDriverManager(nil, nil, nil, mgr))

	raw1 := buildControlTestZIP(t, map[string]string{
		"Net/e1d68x64.inf": "[Version]\nClass=Net\n[Models.NTamd64]\nCopyFiles=e1d68x64.sys\n",
		"Net/e1d68x64.sys": "DELL-SYS-1",
		"VMD/iaStorVD.inf": "[Version]\nClass=SCSIAdapter\n[Models.NTamd64]\nCopyFiles=iaStorVD.sys\n",
		"VMD/iaStorVD.sys": "DELL-VMD-1",
	})
	inv1, err := mgr.Import(context.Background(), bytes.NewReader(raw1), int64(len(raw1)))
	if err != nil {
		t.Fatal(err)
	}

	raw2 := buildControlTestZIP(t, map[string]string{
		"netkvm.inf": "[Version]\nClass=Net\n[Models.NTamd64]\nCopyFiles=netkvm.sys\n",
		"netkvm.sys": "VIRTIO-SYS-2",
	})
	inv2, err := mgr.Import(context.Background(), bytes.NewReader(raw2), int64(len(raw2)))
	if err != nil {
		t.Fatal(err)
	}

	raw3 := buildControlTestZIP(t, map[string]string{
		"rtux64w10.inf": "[Version]\nClass=Net\n[Models.NTamd64]\nCopyFiles=rtux64w10.sys\n",
		"rtux64w10.sys": "REALTEK-SYS-3",
	})
	inv3, err := mgr.Import(context.Background(), bytes.NewReader(raw3), int64(len(raw3)))
	if err != nil {
		t.Fatal(err)
	}

	store := &driverExportTestStore{
		driverPageStore: driverPageStore{
			authOnlyStore: authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true}},
			packages: []DriverPackage{
				{SHA256: inv1.SHA256, Title: "Dell Latitude 5440 / 5450 — сеть и NVMe", Inventory: inv1},
				{SHA256: inv2.SHA256, Title: "VirtIO NetKVM 0.1.262", Inventory: inv2},
				{SHA256: inv3.SHA256, Title: "", Inventory: inv3},
			},
		},
	}
	server := &Server{Store: store, Agent: agentClient}
	handler := server.Handler()

	// 1. Single package export works for admin, operator, and observer, preserving CanonicalHash.
	for _, role := range []string{"admin", "operator", "observer"} {
		store.user.Role = role
		req := httptest.NewRequest(http.MethodGet, "/drivers/packages/"+inv1.SHA256+"/export", nil)
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s single export status = %d, body = %s", role, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
			t.Fatalf("%s Content-Type = %q", role, ct)
		}
		cd := rec.Header().Get("Content-Disposition")
		if !strings.Contains(cd, `filename="driver-`+inv1.SHA256[:8]+`.zip"`) || !strings.Contains(cd, "filename*=UTF-8''") {
			t.Fatalf("%s Cyrillic Content-Disposition = %q", role, cd)
		}
		reimported, err := drivers.InspectZIP(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
		if err != nil {
			t.Fatalf("%s InspectZIP failed: %v", role, err)
		}
		if reimported.SHA256 != inv1.SHA256 {
			t.Fatalf("%s reimported SHA256 = %s, want %s", role, reimported.SHA256, inv1.SHA256)
		}
	}

	// ASCII title uses direct readable filename.
	reqASCII := httptest.NewRequest(http.MethodGet, "/drivers/packages/"+inv2.SHA256+"/export", nil)
	reqASCII.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	recASCII := httptest.NewRecorder()
	handler.ServeHTTP(recASCII, reqASCII)
	if recASCII.Code != http.StatusOK {
		t.Fatalf("ASCII export status = %d", recASCII.Code)
	}
	wantASCII := `attachment; filename="VirtIO NetKVM 0.1.262-` + inv2.SHA256[:8] + `.zip"`
	if got := recASCII.Header().Get("Content-Disposition"); got != wantASCII {
		t.Fatalf("ASCII Content-Disposition = %q, want %q", got, wantASCII)
	}

	// 2. Selective batch export (observer allowed, CSRF enforced, duplicate titles disambiguated).
	store.user.Role = "observer"
	store.packages[1].Title = store.packages[0].Title // identical titles must still get distinct folders via -<sha8>
	csrf := CSRFToken("test-token")

	// Bad CSRF -> 403
	badForm := url.Values{"csrf": {"wrong"}, "item": {inv1.SHA256}}
	reqBad := httptest.NewRequest(http.MethodPost, "/drivers/packages/export", strings.NewReader(badForm.Encode()))
	reqBad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqBad.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	recBad := httptest.NewRecorder()
	handler.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusForbidden {
		t.Fatalf("bad CSRF status = %d, want 403", recBad.Code)
	}

	// 2 selected packages -> omb-drivers-YYYY-MM-DD.zip
	selForm := url.Values{"csrf": {csrf}, "item": {inv1.SHA256, inv2.SHA256}}
	reqSel := httptest.NewRequest(http.MethodPost, "/drivers/packages/export", strings.NewReader(selForm.Encode()))
	reqSel.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSel.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	recSel := httptest.NewRecorder()
	handler.ServeHTTP(recSel, reqSel)
	if recSel.Code != http.StatusOK {
		t.Fatalf("selected export status = %d, body = %s", recSel.Code, recSel.Body.String())
	}
	if !regexp.MustCompile(`^attachment; filename="omb-drivers-\d{4}-\d{2}-\d{2}\.zip"$`).MatchString(recSel.Header().Get("Content-Disposition")) {
		t.Fatalf("selected Content-Disposition = %q", recSel.Header().Get("Content-Disposition"))
	}
	zrSel, err := zip.NewReader(bytes.NewReader(recSel.Body.Bytes()), int64(recSel.Body.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader selected: %v", err)
	}
	topDirs := map[string]bool{}
	for _, f := range zrSel.File {
		top, _, _ := strings.Cut(f.Name, "/")
		topDirs[top] = true
	}
	if len(topDirs) != 2 {
		t.Fatalf("expected 2 distinct top-level folders, got %v", topDirs)
	}

	// 3. Export all -> 3 top-level folders (including untitled driver-<sha8>).
	reqAll := httptest.NewRequest(http.MethodGet, "/drivers/packages/export", nil)
	reqAll.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	recAll := httptest.NewRecorder()
	handler.ServeHTTP(recAll, reqAll)
	if recAll.Code != http.StatusOK {
		t.Fatalf("export all status = %d, body = %s", recAll.Code, recAll.Body.String())
	}
	zrAll, err := zip.NewReader(bytes.NewReader(recAll.Body.Bytes()), int64(recAll.Body.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader all: %v", err)
	}
	allDirs := map[string]bool{}
	for _, f := range zrAll.File {
		top, _, _ := strings.Cut(f.Name, "/")
		allDirs[top] = true
	}
	if len(allDirs) != 3 || !allDirs["driver-"+inv3.SHA256[:8]] {
		t.Fatalf("export all folders = %v", allDirs)
	}

	// Check audit entries recorded.
	foundAllAudit := false
	for _, ev := range store.audits {
		if ev.action == "driver-package.export" && ev.result == "ok" && ev.detail == "count=3 mode=all" {
			foundAllAudit = true
		}
	}
	if !foundAllAudit {
		t.Fatalf("missing audit for export all; got %+v", store.audits)
	}

	// 4. Empty catalog -> 404.
	store.packages = nil
	reqEmpty := httptest.NewRequest(http.MethodGet, "/drivers/packages/export", nil)
	reqEmpty.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	recEmpty := httptest.NewRecorder()
	handler.ServeHTTP(recEmpty, reqEmpty)
	if recEmpty.Code != http.StatusNotFound {
		t.Fatalf("empty export status = %d, want 404", recEmpty.Code)
	}
}

func TestDriversPageExportUI(t *testing.T) {
	usedSHA := strings.Repeat("a", 64)
	freeSHA := strings.Repeat("b", 64)

	store := &driverUsageStore{driverConfigStore: configFixture()}
	store.user = User{ID: 1, Username: "admin", Role: "admin", Enabled: true}
	store.packages = nil
	store.configs = nil

	s := &Server{Store: store}

	// 1. Empty catalog hides "Download all ZIP" and select-all controls.
	emptyPage := libraryGet(t, s, "/drivers")
	if strings.Contains(emptyPage, `href="/drivers/packages/export"`) || strings.Contains(emptyPage, `data-bulk-select-all="driver-packages"`) {
		t.Fatalf("empty /drivers should not show export-all or select-all")
	}

	// 2. Populated catalog: 1 used package + 1 unused package.
	store.packages = []DriverPackage{
		{SHA256: usedSHA, Title: "Dell Pack", Inventory: drivers.Inventory{SHA256: usedSHA, INFs: []drivers.INF{{Path: "net.inf"}}}},
		{SHA256: freeSHA, Title: "VirtIO Pack", Inventory: drivers.Inventory{SHA256: freeSHA, INFs: []drivers.INF{{Path: "vio.inf"}}}},
	}
	store.configs = []DriverConfig{
		{ID: 1, ProfileID: 17, State: "ready", PackageINFs: []agent.SelectedINF{{PackageSHA256: usedSHA, INFPath: "net.inf"}}},
	}

	for _, role := range []string{"admin", "operator", "observer"} {
		store.user.Role = role
		store.user.Language = langRU
		body := libraryGet(t, s, "/drivers")

		for _, want := range []string{
			`href="/drivers/packages/export"`,
			`Скачать все ZIP`,
			`data-bulk-select-all="driver-packages"`,
			`data-bulk-bar="driver-packages"`,
			`id="driver-packages-export-form"`,
			`action="/drivers/packages/export"`,
			`popovertarget="driver-pkg-menu-` + usedSHA + `"`,
			`popovertarget="driver-pkg-menu-` + freeSHA + `"`,
			`href="/drivers/packages/` + usedSHA + `/export"`,
			`href="/drivers/packages/` + freeSHA + `/export"`,
			`data-bulk-value="` + usedSHA + `"`,
			`data-bulk-value="` + freeSHA + `"`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("role %s missing %q in /drivers", role, want)
			}
		}

		// Used package checkbox must NOT be disabled so it can be selected for export,
		// while carrying data-bulk-published="1" so admin bulk delete skips it.
		usedCheckboxRe := regexp.MustCompile(`<input[^>]*data-bulk-value="` + usedSHA + `"[^>]*>`)
		usedCheckbox := usedCheckboxRe.FindString(body)
		if usedCheckbox == "" {
			t.Fatalf("role %s missing checkbox for used package", role)
		}
		if strings.Contains(usedCheckbox, "disabled") {
			t.Fatalf("role %s used package checkbox must not be disabled: %s", role, usedCheckbox)
		}
		if !strings.Contains(usedCheckbox, `data-bulk-published="1"`) {
			t.Fatalf("role %s used package checkbox missing data-bulk-published=1: %s", role, usedCheckbox)
		}

		if role == "admin" {
			if !strings.Contains(body, `id="driver-packages-bulk-delete-dialog"`) || !strings.Contains(body, `data-skip-published="1"`) {
				t.Fatalf("admin missing skip-aware bulk delete dialog")
			}
			if !regexp.MustCompile(`data-driver-package-delete-open="` + usedSHA + `"[^>]*disabled`).MatchString(body) {
				t.Fatalf("admin used package delete item in menu must be disabled")
			}
		} else {
			if strings.Contains(body, `id="driver-packages-bulk-delete-dialog"`) || strings.Contains(body, `data-driver-package-delete-open=`) {
				t.Fatalf("role %s must not see delete controls", role)
			}
		}
	}

	// 3. English translation check.
	store.user.Role = "admin"
	store.user.Language = langEN
	enBody := libraryGet(t, s, "/drivers")
	for _, want := range []string{"Download all ZIP", "Download ZIP", "Select all"} {
		if !strings.Contains(enBody, want) {
			t.Fatalf("EN /drivers missing %q", want)
		}
	}
}
