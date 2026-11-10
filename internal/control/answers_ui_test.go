package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnswersScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/answers.js", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("unexpected Content-Type: %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"answer-upload-dialog", "data-answer-upload-open", "data-close-dialog", "showModal"} {
		if !strings.Contains(body, want) {
			t.Fatalf("answers.js lacks %q", want)
		}
	}
}

func TestAnswersPageLayout(t *testing.T) {
	s := libraryServer()
	body := libraryGet(t, s, "/answers")

	for _, want := range []string{
		`data-answer-upload-open`,
		`href="/ubuntu-autoinstall/new"`,
		`class="btn btn-primary" href="/answers/editor"`,
		`id="answer-upload-dialog"`,
		`class="user-dialog"`,
		`data-close-dialog`,
		`class="autoinstall-section"`,
		`id="ubuntu-autoinstall"`,
		`src="/assets/answers.js"`,
		`action="/answers/upload"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/answers missing %q in body:\n%s", want, body)
		}
	}

	// Verify <details> is no longer used for "Загрузить XML"
	if strings.Contains(body, `<details><summary>Загрузить XML</summary>`) {
		t.Fatalf("/answers still contains <details> for upload XML")
	}
}

func TestAnswersPageObserverOmitsUpload(t *testing.T) {
	s := libraryServer()
	s.Store = authOnlyStore{user: User{ID: 2, Role: "observer", Enabled: true}}
	body := libraryGet(t, s, "/answers")

	if strings.Contains(body, "data-answer-upload-open") {
		t.Fatalf("observer should not see upload button")
	}
	if strings.Contains(body, "id=\"answer-upload-dialog\"") {
		t.Fatalf("observer should not see upload dialog")
	}
	if strings.Contains(body, `href="/ubuntu-autoinstall/new"`) {
		t.Fatalf("observer should not see create ubuntu button")
	}
}
