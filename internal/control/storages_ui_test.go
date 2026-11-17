package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"omb/internal/images"
)

func TestStoragesScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/storages.js", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("unexpected Content-Type: %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"smb-create-dialog", "data-smb-add",
		"nfs-create-dialog", "data-nfs-add",
		"nfs-retry-dialog", "data-nfs-retry-id",
		"storage-delete-dialog", "data-storage-delete-id",
		"data-close-dialog",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("storages.js lacks %q", want)
		}
	}
}

func TestStoragesPageConnectStorageMenuAndModals(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		s := sampleSpaceTestServer("admin", lang, fakeDiskUsageAPI{})
		body := libraryGet(t, s, "/storages")

		for _, want := range []string{
			`popovertarget="storage-connect-menu"`,
			`id="storage-connect-menu"`,
			`data-smb-add`,
			`data-nfs-add`,
			`id="smb-create-dialog"`,
			`action="/images/smb"`,
			`id="nfs-create-dialog"`,
			`action="/images/nfs"`,
			`name="server"`,
			`name="export_path"`,
			`name="version"`,
			`data-close-dialog`,
			`src="/assets/storages.js"`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("/storages (%s) missing %q", lang, want)
			}
		}
		if lang == langRU {
			for _, want := range []string{"Подключить хранилище", "Подключить SMB", "Подключить NFS", "Версия протокола"} {
				if !strings.Contains(body, want) {
					t.Fatalf("/storages (ru) missing %q", want)
				}
			}
		}
		if lang == langEN {
			for _, want := range []string{"Connect storage", "Connect SMB", "Connect NFS", "Protocol version"} {
				if !strings.Contains(body, want) {
					t.Fatalf("/storages (en) missing %q", want)
				}
			}
		}

		if mainStart := strings.Index(body, `<main class="content">`); mainStart < 0 {
			t.Fatalf("/storages (%s) missing <main>", lang)
		} else if mainBody := body[mainStart:]; strings.Contains(mainBody, "<details") || strings.Contains(mainBody, "<summary") {
			t.Fatalf("/storages (%s) must not contain any <details> or <summary> inside <main>", lang)
		}
	}

	obs := sampleSpaceTestServer("observer", langRU, fakeDiskUsageAPI{})
	obsBody := libraryGet(t, obs, "/storages")
	for _, forbidden := range []string{`data-smb-add`, `data-nfs-add`, `id="smb-create-dialog"`, `id="nfs-create-dialog"`, `popovertarget="storage-connect-menu"`} {
		if strings.Contains(obsBody, forbidden) {
			t.Fatalf("observer should not see %q", forbidden)
		}
	}
}

func TestRowMenuScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/row-menu.js", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("unexpected Content-Type: %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"data-row-menu", "popover-open", "data-close-popover"} {
		if !strings.Contains(body, want) {
			t.Fatalf("row-menu.js lacks %q", want)
		}
	}
}

func TestStoragesPageUsesRowMenuAndRetryModalWithNoDetails(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		s := sampleSpaceTestServer("admin", lang, fakeDiskUsageAPI{})
		body := libraryGet(t, s, "/storages")

		for _, want := range []string{
			`data-row-menu`,
			`popovertarget="storage-menu-5"`,
			`id="storage-menu-5"`,
			`popovertarget="storage-menu-6"`,
			`id="storage-menu-6"`,
			`>☰<`,
			`data-smb-retry-id="5"`,
			`data-nfs-retry-id="6"`,
			`data-nfs-retry-version="4.1"`,
			`data-nfs-retry-location="192.168.10.20:/volume1/iso"`,
			`data-storage-delete-id="5"`,
			`data-storage-delete-id="6"`,
			`data-storage-delete-kind="smb"`,
			`data-storage-delete-kind="nfs"`,
			`id="smb-retry-dialog"`,
			`id="nfs-retry-dialog"`,
			`id="storage-delete-dialog"`,
			`id="storage-delete-form"`,
			`class="storage-delete-warning"`,
			`192.168.10.20:/volume1/iso`,
			`src="/assets/row-menu.js"`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("/storages (%s) missing %q", lang, want)
			}
		}
		if lang == langRU && !strings.Contains(body, "SMB и NFS") {
			t.Fatalf("/storages (ru) recovery dialog missing 'SMB и NFS'")
		}
		if lang == langEN && !strings.Contains(body, "SMB and NFS") {
			t.Fatalf("/storages (en) recovery dialog missing 'SMB and NFS'")
		}
		if mainStart := strings.Index(body, `<main class="content">`); mainStart < 0 {
			t.Fatalf("/storages (%s) missing <main>", lang)
		} else if mainBody := body[mainStart:]; strings.Contains(mainBody, "<details") || strings.Contains(mainBody, "<summary") {
			t.Fatalf("/storages (%s) must not contain any <details> or <summary> inside <main>: %s", lang, mainBody)
		}
	}

	op := sampleSpaceTestServer("operator", langRU, fakeDiskUsageAPI{})
	opBody := libraryGet(t, op, "/storages")
	if !strings.Contains(opBody, `action="/images/scan"`) || !strings.Contains(opBody, `data-row-menu`) ||
		strings.Contains(opBody, `data-smb-retry-id=`) || strings.Contains(opBody, `data-nfs-retry-id=`) ||
		strings.Contains(opBody, `id="smb-retry-dialog"`) || strings.Contains(opBody, `id="nfs-retry-dialog"`) ||
		strings.Contains(opBody, `data-storage-delete-id=`) || strings.Contains(opBody, `id="storage-delete-dialog"`) {
		t.Fatalf("operator permissions mismatch on /storages")
	}

	obs := sampleSpaceTestServer("observer", langRU, fakeDiskUsageAPI{})
	obsBody := libraryGet(t, obs, "/storages")
	if strings.Contains(obsBody, `action="/images/scan"`) || strings.Contains(obsBody, `data-row-menu`) ||
		strings.Contains(obsBody, `data-storage-delete-id=`) || strings.Contains(obsBody, `id="storage-delete-dialog"`) {
		t.Fatalf("observer permissions mismatch on /storages")
	}
}

func TestStoragesPageShowsTypeAndAccessMode(t *testing.T) {
	for _, tc := range []struct {
		lang           string
		wantTypeSMB    string
		wantTypeNFSv4  string
		wantTypeNFSv3  string
		wantTypeLocal  string
		wantAccessRW   string
		wantAccessRO   string
		forbidOldLabel string
		forbidOldNote  string
	}{
		{
			lang:           langRU,
			wantTypeSMB:    "SMB",
			wantTypeNFSv4:  "NFSv4.1",
			wantTypeNFSv3:  "NFSv3",
			wantTypeLocal:  "Локальный каталог",
			wantAccessRW:   "Доступно<br><small class=\"muted\">чтение и запись</small>",
			wantAccessRO:   "Доступно<br><small class=\"muted\">только чтение</small>",
			forbidOldLabel: "SMB хранилище",
			forbidOldNote:  "можно загружать ISO",
		},
		{
			lang:           langEN,
			wantTypeSMB:    "SMB",
			wantTypeNFSv4:  "NFSv4.1",
			wantTypeNFSv3:  "NFSv3",
			wantTypeLocal:  "Local directory",
			wantAccessRW:   "Available<br><small class=\"muted\">read and write</small>",
			wantAccessRO:   "Available<br><small class=\"muted\">read-only</small>",
			forbidOldLabel: "SMB storage",
			forbidOldNote:  "ISO uploads allowed",
		},
	} {
		api := &fakeImagesAPI{
			catalog: images.Catalog{
				Storages: []images.Storage{
					{
						ID:      5,
						Name:    "NAS Synology",
						Kind:    "smb",
						Server:  "192.168.10.10",
						Share:   "Downloads",
						State:   "ready",
						Uploads: true,
						Total:   385 << 30,
						Free:    115 << 30,
					},
					{
						ID:         6,
						Name:       "NFS Share v4",
						Kind:       "nfs",
						Server:     "192.168.10.20",
						ExportPath: "/iso4",
						NFSVersion: "4.1",
						State:      "ready",
						Uploads:    false,
						Total:      200 << 30,
						Free:       100 << 30,
					},
					{
						ID:         7,
						Name:       "NFS Share v3",
						Kind:       "nfs",
						Server:     "192.168.10.21",
						ExportPath: "/iso3",
						NFSVersion: "3",
						State:      "ready",
						Uploads:    false,
						Total:      150 << 30,
						Free:       50 << 30,
					},
					{
						ID:      8,
						Name:    "Local Archive",
						Kind:    "local",
						Root:    "/var/lib/omb-images/iso",
						State:   "ready",
						Uploads: false,
						Total:   100 << 30,
						Free:    40 << 30,
					},
				},
			},
		}
		s := &Server{
			Store:     authOnlyStore{user: User{ID: 1, Role: "admin", Language: tc.lang, Enabled: true}},
			Images:    api,
			DiskUsage: fakeDiskUsageAPI{},
		}
		body := libraryGet(t, s, "/storages")

		for _, want := range []string{tc.wantTypeSMB, tc.wantTypeNFSv4, tc.wantTypeNFSv3, tc.wantTypeLocal, tc.wantAccessRW, tc.wantAccessRO} {
			if !strings.Contains(body, want) {
				t.Fatalf("/storages (%s) missing %q in body:\n%s", tc.lang, want, body)
			}
		}
		if strings.Contains(body, tc.forbidOldLabel) {
			t.Fatalf("/storages (%s) must not contain obsolete label %q", tc.lang, tc.forbidOldLabel)
		}
		if strings.Contains(body, tc.forbidOldNote) {
			t.Fatalf("/storages (%s) must not contain obsolete note %q", tc.lang, tc.forbidOldNote)
		}
	}
}

func TestStoragesDeleteConfirmationModal(t *testing.T) {
	for _, tc := range []struct {
		lang        string
		wantTitle   string
		wantWarn    string
		wantDesc    string
		wantHint    string
		wantConfirm string
		wantCancel  string
	}{
		{
			lang:        langRU,
			wantTitle:   "Удалить хранилище",
			wantWarn:    "Внимание",
			wantDesc:    "Связанные образы ISO будут удалены из библиотеки OMB. Если образы используются активными загрузками Windows, сервер заблокирует удаление.",
			wantHint:    "Исходные файлы на сервере останутся без изменений.",
			wantConfirm: "Подтвердить удаление",
			wantCancel:  "Отмена",
		},
		{
			lang:        langEN,
			wantTitle:   "Delete storage",
			wantWarn:    "Warning",
			wantDesc:    "Linked ISO images will be removed from the OMB library. If the images are used by active Windows boot entries, the server will block deletion.",
			wantHint:    "Original files on the server will remain unchanged.",
			wantConfirm: "Confirm deletion",
			wantCancel:  "Cancel",
		},
	} {
		s := sampleSpaceTestServer("admin", tc.lang, fakeDiskUsageAPI{})
		body := libraryGet(t, s, "/storages")

		for _, want := range []string{
			`id="storage-delete-dialog"`,
			`id="storage-delete-form"`,
			`id="storage-delete-name"`,
			`id="storage-delete-path"`,
			tc.wantTitle,
			tc.wantWarn,
			tc.wantDesc,
			tc.wantHint,
			tc.wantConfirm,
			tc.wantCancel,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("/storages (%s) missing %q in delete dialog", tc.lang, want)
			}
		}
	}
}

func TestNFSStorageUIBrowser(t *testing.T) {
	if os.Getenv("OMB_UI_TEST") != "1" {
		t.Skip("set OMB_UI_TEST=1 and NODE_PATH to run browser check")
	}
	dir := t.TempDir()
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"?]+)"`)
	for _, lang := range []string{langRU, langEN} {
		s := sampleSpaceTestServer("admin", lang, fakeDiskUsageAPI{})
		body := libraryGet(t, s, "/storages")
		if err := os.WriteFile(filepath.Join(dir, "admin-"+lang+".html"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		for _, match := range assets.FindAllStringSubmatch(body, -1) {
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, match[1], nil))
			assetPath := filepath.Join(dir, match[1])
			if err := os.MkdirAll(filepath.Dir(assetPath), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(assetPath, res.Body.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/test-nfs-ui.cjs", dir)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser test failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "NFS_UI_BROWSER_TESTS_PASS") {
		t.Fatalf("browser check missing success marker: %s", string(out))
	}
}
