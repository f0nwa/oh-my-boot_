package control

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pxe-platform/internal/boot"
)

func TestDashboardEscapesClientEventText(t *testing.T) {
	res := httptest.NewRecorder()
	render(res, "dashboard", pageData{Info: requestContext{User: User{ID: 1, Username: "viewer", Role: "observer"}}, Events: []boot.Event{{At: time.Now(), Path: "<script>alert(1)</script>", Kind: "menu"}}})
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
