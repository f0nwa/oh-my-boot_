package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAsyncBlockScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/async-block.js", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("unexpected Content-Type: %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("unexpected Cache-Control: %q", cc)
	}

	body := rec.Body.String()
	requiredSignatures := []string{
		"loadAsyncBlock",
		"cancelAsyncBlock",
		"data-async-url",
		"data-async-retry",
		"AbortController",
		"pagehide",
		"aria-busy",
		"async-block-loading",
		"async-block-error",
		"initInfiniteScroll",
		"menu-versions-dialog",
		"data-menu-versions-open",
		"Не удалось загрузить данные",
		"Failed to load data",
	}
	for _, sig := range requiredSignatures {
		if !strings.Contains(body, sig) {
			t.Errorf("async-block.js missing expected signature %q", sig)
		}
	}
}

func TestAsyncBlockScriptIncludedInPages(t *testing.T) {
	expectedTag := `<script defer src="/assets/async-block.js"></script>`

	for _, page := range []string{"now", "menu", "installations", "events", "machine"} {
		for _, lang := range []string{langRU, langEN} {
			data := samplePageData(lang)
			if page == "now" {
				data.Now = &nowView{
					Services: []nowService{
						{Name: "dnsmasq", State: "ok", Value: "10.0.0.1"},
					},
				}
			}
			body := renderPage(page, data)
			if !strings.Contains(body, expectedTag) {
				t.Errorf("page %q (lang=%s) missing async-block script tag %q", page, lang, expectedTag)
			}
		}
	}
}

func TestAsyncBlockStyles(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/style.css", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	css := rec.Body.String()
	requiredClasses := []string{
		".async-block-loading",
		".async-block-error",
		".async-block-error .error",
		".async-block-error .btn",
		".changes-card",
	}
	for _, cls := range requiredClasses {
		if !strings.Contains(css, cls) {
			t.Errorf("style.css missing expected class %q", cls)
		}
	}
}
