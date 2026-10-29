package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPageErrorsRenderFailedFormOnItsPage(t *testing.T) {
	page := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("page rendered for %s", r.Method)
		}
		render(w, "menu", pageData{Error: pageError(r)})
	}
	failing := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Профиль Windows ISO недоступен: хранилище «DSM» недоступно", http.StatusConflict)
	}
	res := httptest.NewRecorder()
	withPageErrors(page, failing)(res, httptest.NewRequest(http.MethodPost, "/menu/publish", nil))
	body := res.Body.String()
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", res.Code)
	}
	if !strings.HasPrefix(res.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("content type = %q", res.Header().Get("Content-Type"))
	}
	for _, want := range []string{"Меню загрузки", `role="alert"`, "хранилище «DSM» недоступно"} {
		if !strings.Contains(body, want) {
			t.Fatalf("page lacks %q: %s", want, body)
		}
	}
}

func TestPageErrorsPassSuccessfulResponsesThrough(t *testing.T) {
	page := func(w http.ResponseWriter, r *http.Request) { t.Error("page rendered for successful form") }
	redirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/menu?message=ok", http.StatusSeeOther)
	}
	res := httptest.NewRecorder()
	withPageErrors(page, redirect)(res, httptest.NewRequest(http.MethodPost, "/menu/publish", nil))
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/menu?message=ok" {
		t.Fatalf("redirect changed: %d %q", res.Code, res.Header().Get("Location"))
	}
}

func TestPageErrorIsNotTakenFromQuery(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/menu?error=fake", nil)
	if got := pageError(r); got != "" {
		t.Fatalf("page error from query: %q", got)
	}
}

func TestMenuFormNoLongerOffersStaticWindowsItems(t *testing.T) {
	res := httptest.NewRecorder()
	render(res, "menu", pageData{Info: requestContext{User: User{ID: 1, Role: "admin"}, CSRF: "x"}, Preview: &menuPreview{Key: "ubuntu", Builtin: "Ubuntu Desktop"}})
	body := res.Body.String()
	for _, gone := range []string{`name="windows10"`, `name="windows11"`, `name="folder_windows10"`, `name="folder_windows11"`} {
		if strings.Contains(body, gone) {
			t.Errorf("menu form still offers %s", gone)
		}
	}
	if !strings.Contains(body, `name="ubuntu"`) || !strings.Contains(body, `name="folder_ubuntu"`) {
		t.Fatal("Ubuntu item disappeared from the menu form")
	}
}
