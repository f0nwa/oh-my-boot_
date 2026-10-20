package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func navPaths(groups []navGroup) map[string]bool {
	paths := map[string]bool{}
	for _, group := range groups {
		for _, item := range group.Items {
			paths[item.Path] = true
		}
	}
	return paths
}

func TestNavigationHidesItemsWithoutPermission(t *testing.T) {
	observer := navPaths(navigation("observer", "/"))
	if observer["/users"] || observer["/audit"] {
		t.Fatalf("observer sees admin-only items: %v", observer)
	}
	admin := navPaths(navigation("admin", "/"))
	if !admin["/users"] || !admin["/audit"] {
		t.Fatalf("admin misses items: %v", admin)
	}
}

func TestNavigationMarksActiveItem(t *testing.T) {
	for path, want := range map[string]string{"/images": "/images", "/": "/"} {
		var active []string
		for _, group := range navigation("admin", path) {
			for _, item := range group.Items {
				if item.Active {
					active = append(active, item.Path)
				}
			}
		}
		if len(active) != 1 || active[0] != want {
			t.Fatalf("path %s: active=%v", path, active)
		}
	}
}

func TestLayoutServesStylesheet(t *testing.T) {
	res := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/assets/style.css", nil))
	if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "text/css") || !strings.Contains(res.Body.String(), "#E4E4E7") {
		t.Fatalf("response=%d %q", res.Code, res.Header().Get("Content-Type"))
	}
}

func TestContentSecurityPolicyAllowsOwnStylesheet(t *testing.T) {
	res := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/assets/style.css", nil))
	csp := res.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "style-src 'self'") {
		t.Fatalf("CSP blocks /assets/style.css: %q", csp)
	}
}
