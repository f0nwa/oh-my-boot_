package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMenuSettingsIgnoreTitleField(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready"}
	s, _ := draftServer(t, store)
	res := menuPost(t, s, "/menu/diagnostics", url.Values{"title": {"Other name"}, "label": {"Diagnostics"}, "message": {"Ready"}})
	if res.Code != http.StatusSeeOther || store.draft.Title != productMenuTitle {
		t.Fatalf("status=%d title=%q body=%s", res.Code, store.draft.Title, res.Body.String())
	}
}

// An installation upgraded from the old name keeps its published menu until
// someone publishes; the draft already carries the product name and the new
// default message, so the rename shows as a pending change.
func TestOldMenuNameIsReplacedOnlyByPublishing(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	store.versions[0].Title, store.versions[0].Message = "PXE Platform", legacyMenuMessage
	store.draft = &MenuDraft{Title: "PXE Platform", Label: "Diagnostics", Message: legacyMenuMessage, Items: []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}}}
	s, published := draftServer(t, store)
	draft, active, err := s.currentDraft(context.Background())
	if err != nil || draft.Title != productMenuTitle || draft.Message != defaultMenuMessage || active.Title != "PXE Platform" || len(diffMenuDraft(active, draft)) == 0 {
		t.Fatalf("err=%v draft=%+v active=%+v", err, draft, active)
	}
	if len(*published) != 0 {
		t.Fatal("the active menu changed without publishing")
	}
	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther || len(*published) != 1 || !strings.Contains((*published)[0], "menu "+productMenuTitle) || strings.Contains((*published)[0], "PXE Platform") {
		t.Fatalf("status=%d published=%v body=%s", res.Code, *published, res.Body.String())
	}
}

func TestMenuPageHasNoTitleField(t *testing.T) {
	res := httptest.NewRecorder()
	data := samplePageData(langRU)
	data.Preview.Key, data.Preview.Builtin = "diagnostics", "Diagnostics"
	data.Preview.Item = nil
	render(res, "menu", data)
	body := res.Body.String()
	start := strings.Index(body, `action="/menu/diagnostics"`)
	if start < 0 {
		t.Fatal("no menu settings form")
	}
	form := body[start:]
	form = form[:strings.Index(form, "</form>")]
	if strings.Contains(form, `name="title"`) {
		t.Fatal("the menu settings still offer a title field")
	}
}

func TestPagesShowProductName(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		for _, name := range []string{"login", "installations", "menu", "installation_new"} {
			res := httptest.NewRecorder()
			render(res, name, samplePageData(lang))
			body := res.Body.String()
			if strings.Contains(body, "PXE Platform") || !strings.Contains(body, "<title>Oh My Boot</title>") || !strings.Contains(body, `href="/assets/favicon.svg"`) {
				t.Errorf("%s/%s does not show the product name", lang, name)
			}
		}
	}
	res := httptest.NewRecorder()
	render(res, "login", samplePageData(langEN))
	if !strings.Contains(res.Body.String(), "Sign in to Oh My Boot") || !strings.Contains(res.Body.String(), `class="wordmark" aria-hidden="true"`) {
		t.Fatalf("login: %s", res.Body.String())
	}
}

func TestBrandIconsAreServedWithoutSession(t *testing.T) {
	for path, want := range map[string]string{"/favicon.ico": "image/x-icon", "/assets/favicon.svg": "image/svg+xml", "/assets/apple-touch-icon.png": "image/png"} {
		res := httptest.NewRecorder()
		(&Server{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK || res.Header().Get("Content-Type") != want || res.Body.Len() < 100 {
			t.Errorf("%s: status=%d type=%q len=%d", path, res.Code, res.Header().Get("Content-Type"), res.Body.Len())
		}
	}
}
