package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	for _, want := range []string{"smb-create-dialog", "data-smb-add", "data-close-dialog"} {
		if !strings.Contains(body, want) {
			t.Fatalf("storages.js lacks %q", want)
		}
	}
}

func TestStoragesPageAddSMBModal(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		s := sampleSpaceTestServer("admin", lang, fakeDiskUsageAPI{})
		body := libraryGet(t, s, "/storages")

		for _, want := range []string{
			`data-smb-add`,
			`id="smb-create-dialog"`,
			`class="user-dialog"`,
			`action="/images/smb"`,
			`data-close-dialog`,
			`src="/assets/storages.js"`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("/storages (%s) missing %q", lang, want)
			}
		}
		if lang == langRU && !strings.Contains(body, "Подключить SMB хранилище") {
			t.Fatalf("/storages (ru) missing renamed button label")
		}
		if lang == langEN && !strings.Contains(body, "Connect SMB storage") {
			t.Fatalf("/storages (en) missing translated button label")
		}

		// Ensure neither the SMB add wrapper nor the domain field uses <details>/<summary>.
		if strings.Contains(body, `<summary>Добавить сетевую папку SMB</summary>`) ||
			strings.Contains(body, `<summary>Add an SMB network share</summary>`) ||
			strings.Contains(body, `<summary>Домен, если нужен</summary>`) ||
			strings.Contains(body, `<summary>Domain, if required</summary>`) {
			t.Fatalf("/storages (%s) still renders <details>/<summary> for SMB creation", lang)
		}
	}

	// Non-admins must not get the add button or modal dialog.
	obs := sampleSpaceTestServer("observer", langRU, fakeDiskUsageAPI{})
	obsBody := libraryGet(t, obs, "/storages")
	if strings.Contains(obsBody, `data-smb-add`) || strings.Contains(obsBody, `id="smb-create-dialog"`) {
		t.Fatalf("observer should not see add SMB button or dialog")
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
			`>☰<`,
			`data-smb-retry-id="5"`,
			`data-smb-delete-id="5"`,
			`id="smb-retry-dialog"`,
			`id="smb-delete-dialog"`,
			`id="smb-delete-form"`,
			`class="storage-delete-warning"`,
			`src="/assets/row-menu.js"`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("/storages (%s) missing %q", lang, want)
			}
		}
		if mainStart := strings.Index(body, `<main class="content">`); mainStart < 0 {
			t.Fatalf("/storages (%s) missing <main>", lang)
		} else if mainBody := body[mainStart:]; strings.Contains(mainBody, "<details") || strings.Contains(mainBody, "<summary") {
			t.Fatalf("/storages (%s) must not contain any <details> or <summary> inside <main>: %s", lang, mainBody)
		}
	}

	op := sampleSpaceTestServer("operator", langRU, fakeDiskUsageAPI{})
	opBody := libraryGet(t, op, "/storages")
	if !strings.Contains(opBody, `action="/images/scan"`) || !strings.Contains(opBody, `data-row-menu`) || strings.Contains(opBody, `data-smb-retry-id=`) || strings.Contains(opBody, `id="smb-retry-dialog"`) || strings.Contains(opBody, `data-smb-delete-id=`) || strings.Contains(opBody, `id="smb-delete-dialog"`) {
		t.Fatalf("operator permissions mismatch on /storages")
	}

	obs := sampleSpaceTestServer("observer", langRU, fakeDiskUsageAPI{})
	obsBody := libraryGet(t, obs, "/storages")
	if strings.Contains(obsBody, `action="/images/scan"`) || strings.Contains(obsBody, `data-row-menu`) || strings.Contains(obsBody, `data-smb-delete-id=`) || strings.Contains(obsBody, `id="smb-delete-dialog"`) {
		t.Fatalf("observer permissions mismatch on /storages")
	}
}

func TestStoragesPageShowsTypeAndAccessMode(t *testing.T) {
	for _, tc := range []struct {
		lang          string
		wantTypeSMB   string
		wantTypeLocal string
		wantAccessRW  string
		wantAccessRO  string
		forbidOldNote string
	}{
		{
			lang:          langRU,
			wantTypeSMB:   "SMB хранилище",
			wantTypeLocal: "Локальный каталог",
			wantAccessRW:  "Доступно<br><small class=\"muted\">чтение и запись</small>",
			wantAccessRO:  "Доступно<br><small class=\"muted\">только чтение</small>",
			forbidOldNote: "можно загружать ISO",
		},
		{
			lang:          langEN,
			wantTypeSMB:   "SMB storage",
			wantTypeLocal: "Local directory",
			wantAccessRW:  "Available<br><small class=\"muted\">read and write</small>",
			wantAccessRO:  "Available<br><small class=\"muted\">read-only</small>",
			forbidOldNote: "ISO uploads allowed",
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
						ID:      6,
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

		for _, want := range []string{tc.wantTypeSMB, tc.wantTypeLocal, tc.wantAccessRW, tc.wantAccessRO} {
			if !strings.Contains(body, want) {
				t.Fatalf("/storages (%s) missing %q in body:\n%s", tc.lang, want, body)
			}
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
			wantHint:    "Исходные файлы на сервере SMB останутся без изменений.",
			wantConfirm: "Подтвердить удаление",
			wantCancel:  "Отмена",
		},
		{
			lang:        langEN,
			wantTitle:   "Delete storage",
			wantWarn:    "Warning",
			wantDesc:    "Linked ISO images will be removed from the OMB library. If the images are used by active Windows boot entries, the server will block deletion.",
			wantHint:    "Original files on the SMB server will remain unchanged.",
			wantConfirm: "Confirm deletion",
			wantCancel:  "Cancel",
		},
	} {
		s := sampleSpaceTestServer("admin", tc.lang, fakeDiskUsageAPI{})
		body := libraryGet(t, s, "/storages")

		for _, want := range []string{
			`id="smb-delete-dialog"`,
			`id="smb-delete-form"`,
			`id="smb-delete-name"`,
			`id="smb-delete-path"`,
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
