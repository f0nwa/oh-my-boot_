package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
