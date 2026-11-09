package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStylesheetCustomSelectChevron(t *testing.T) {
	data, err := templateFS.ReadFile("templates/style.css")
	if err != nil {
		t.Fatalf("failed to read templates/style.css: %v", err)
	}
	css := string(data)

	// 1. Check appearance: none & webkit appearance for select
	for _, want := range []string{
		"appearance: none",
		"-webkit-appearance: none",
		"padding-right: 30px",
		"background-repeat: no-repeat",
		"background-position: right 10px center",
		"background-size: 11px 11px",
		"cursor: pointer",
		"data:image/svg+xml",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet lacks %q for custom select", want)
		}
	}

	// 2. Check light theme selector
	if !strings.Contains(css, `[data-theme="light"] select`) && !strings.Contains(css, `[data-theme='light'] select`) {
		t.Errorf("stylesheet lacks light theme select rule")
	}

	// 3. Check hover / focus states
	if !strings.Contains(css, "select:hover") || !strings.Contains(css, "select:focus") {
		t.Errorf("stylesheet lacks hover / focus rules for select")
	}

	// 4. Check compact table select (.user-table td select)
	if !strings.Contains(css, ".user-table td select") {
		t.Errorf("stylesheet lacks .user-table td select rule")
	}
	if !strings.Contains(css, "26px") {
		t.Errorf("compact table select lacks 26px padding-right")
	}
	if !strings.Contains(css, "right 8px center") {
		t.Errorf("compact table select lacks 'right 8px center' positioning")
	}
}

func TestContentSecurityPolicyAllowsDataImagesForSelectChevron(t *testing.T) {
	s := &Server{}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/assets/style.css", nil))
	csp := res.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "img-src 'self' data:") {
		t.Fatalf("CSP does not permit data: images for select chevron: %q", csp)
	}
}
