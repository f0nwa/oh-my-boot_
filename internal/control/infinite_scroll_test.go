package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInfiniteScrollScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/infinite-scroll.js", nil)
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
	for _, want := range []string{
		"IntersectionObserver",
		"rootMargin",
		"infinite-scroll-sentinel",
		"data-infinite-retry",
		"initInfiniteScroll",
		"AbortController",
		"pagehide",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("infinite_scroll.js lacks %q", want)
		}
	}
}

func TestInfiniteScrollScriptIncludedInPages(t *testing.T) {
	s := &Server{
		Store: newRulesBook("admin"),
	}

	// 1. Проверяем страницу /machines
	bodyMachines := libraryGet(t, s, "/machines")
	if !strings.Contains(bodyMachines, `<script defer src="/assets/infinite-scroll.js"></script>`) {
		t.Errorf("machines page lacks script tag for infinite-scroll.js")
	}
	if mainIdx := strings.Index(bodyMachines, "<main"); mainIdx >= 0 {
		mainBody := bodyMachines[mainIdx:]
		if strings.Contains(mainBody, "<details") || strings.Contains(mainBody, "<summary") {
			t.Errorf("machines page must not contain <details> or <summary> inside <main>")
		}
	}

	// 2. Проверяем страницу /events
	bodyEvents := libraryGet(t, s, "/events?full=1&tab=requests")
	if !strings.Contains(bodyEvents, `<script defer src="/assets/infinite-scroll.js"></script>`) {
		t.Errorf("events page lacks script tag for infinite-scroll.js")
	}
	if mainIdx := strings.Index(bodyEvents, "<main"); mainIdx >= 0 {
		mainBody := bodyEvents[mainIdx:]
		if strings.Contains(mainBody, "<details") || strings.Contains(mainBody, "<summary") {
			t.Errorf("events page must not contain <details> or <summary> inside <main>")
		}
	}
}
