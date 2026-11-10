package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEventsScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/events.js", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("unexpected Content-Type: %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"journal-clear-dialog", "data-journal-clear-open", "data-close-dialog", "journal-days", "btn-primary"} {
		if !strings.Contains(body, want) {
			t.Fatalf("events.js lacks %q", want)
		}
	}
}
