package control

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pxe-platform/internal/boot"
)

func TestEventsEscapesClientEventText(t *testing.T) {
	res := httptest.NewRecorder()
	render(res, "events", pageData{Tab: "requests", Info: requestContext{User: User{ID: 1, Username: "viewer", Role: "observer"}}, Events: []boot.Event{{At: time.Now(), Path: "<script>alert(1)</script>", Kind: "menu"}}})
	if res.Code != 200 {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "<script>") || !strings.Contains(res.Body.String(), "&lt;script&gt;") {
		t.Fatalf("event was not escaped: %s", res.Body.String())
	}
}

func TestLoginPageUsesSecureCSRFCookie(t *testing.T) {
	s := &Server{}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest("GET", "/login", nil))
	if res.Code != 200 {
		t.Fatalf("status=%d", res.Code)
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Value == "" {
		t.Fatalf("cookies=%+v", cookies)
	}
}

func TestEveryPageUsesPageHead(t *testing.T) {
	for _, name := range []string{"login", "installations", "installation_new", "account", "events", "network", "network_preview", "menu", "users", "audit", "images", "answers", "storages", "drivers"} {
		res := httptest.NewRecorder()
		render(res, name, samplePageData(langRU))
		body := res.Body.String()
		if !strings.Contains(body, `class="page-head"`) {
			t.Errorf("%s has no page head", name)
		}
		if n := strings.Count(body, `class="btn btn-primary"`); n > 1 {
			t.Errorf("%s has %d primary buttons", name, n)
		}
	}
}
