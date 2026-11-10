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
			`action="/images/smb/5/remove"`,
			`id="smb-retry-dialog"`,
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
	if !strings.Contains(opBody, `action="/images/scan"`) || strings.Contains(opBody, `data-row-menu`) || strings.Contains(opBody, `id="smb-retry-dialog"`) {
		t.Fatalf("operator permissions mismatch on /storages")
	}

	obs := sampleSpaceTestServer("observer", langRU, fakeDiskUsageAPI{})
	obsBody := libraryGet(t, obs, "/storages")
	if strings.Contains(obsBody, `action="/images/scan"`) || strings.Contains(obsBody, `data-row-menu`) {
		t.Fatalf("observer permissions mismatch on /storages")
	}
}
