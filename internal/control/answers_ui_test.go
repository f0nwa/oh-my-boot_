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
		`data-row-menu`,
		`popovertarget="answers-add-menu"`,
		`id="answers-add-menu"`,
		`class="row-menu-popover"`,
		`href="/answers/editor"`,
		`data-answer-upload-open`,
		`href="/ubuntu-autoinstall/new"`,
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

	// Verify <details> is no longer used for "Загрузить XML" or "Проверить"
	if strings.Contains(body, `<details><summary>Загрузить XML</summary>`) {
		t.Fatalf("/answers still contains <details> for upload XML")
	}
	if strings.Contains(body, `<th>Диск</th>`) {
		t.Fatalf("/answers should not contain separate Disk table header")
	}
	if strings.Contains(body, `Проверить`) {
		t.Fatalf("/answers should not contain inspect/verify button in table")
	}
}

func TestAnswersPageWipesDiskChip(t *testing.T) {
	for _, tc := range []struct {
		lang      string
		wantBadge string
	}{
		{langRU, `<span class="chip-bad">стирает диск</span>`},
		{langEN, `<span class="chip-bad">wipes the disk</span>`},
	} {
		body := renderPage("answers", samplePageData(tc.lang))
		if !strings.Contains(body, tc.wantBadge) {
			t.Fatalf("expected danger badge %q for lang %s, got:\n%s", tc.wantBadge, tc.lang, body)
		}
	}
}

func TestAnswersPageObserverOmitsActions(t *testing.T) {
	s := libraryServer()
	s.Store = authOnlyStore{user: User{ID: 2, Role: "observer", Enabled: true}}
	body := libraryGet(t, s, "/answers")

	if strings.Contains(body, "answers-add-menu") {
		t.Fatalf("observer should not see add menu")
	}
	if strings.Contains(body, "data-answer-upload-open") {
		t.Fatalf("observer should not see upload trigger")
	}
	if strings.Contains(body, "id=\"answer-upload-dialog\"") {
		t.Fatalf("observer should not see upload dialog")
	}
	if strings.Contains(body, `href="/ubuntu-autoinstall/new"`) {
		t.Fatalf("observer should not see create ubuntu button")
	}
}

func TestAnswerRowActions(t *testing.T) {
	for _, role := range []string{"admin", "operator", "observer"} {
		data := samplePageData(langRU)
		data.Info.User.Role = role
		body := renderPage("answers", data)
		for _, want := range []string{`popovertarget="answer-menu-5"`, `id="answer-menu-5"`, `href="/answers/5/edit"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %s", role, want)
			}
		}
		hasDelete := strings.Contains(body, `action="/images/windows-answers/5/delete"`)
		if hasDelete != (role == "admin") {
			t.Errorf("%s: unexpected delete availability", role)
		}
		if strings.Contains(body, `<summary>Удалить сценарий #`) {
			t.Errorf("%s: obsolete disclosure", role)
		}
	}
}
