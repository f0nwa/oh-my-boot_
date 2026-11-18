package control

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNoticeScriptEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	noticeScript(rec, httptest.NewRequest("GET", "/assets/notice.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("status=%d type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	for _, want := range []string{"data-notice-close", "data-notice-auto", "animationend", "is-hiding"} {
		if !strings.Contains(body, want) {
			t.Errorf("notice.js does not contain %q", want)
		}
	}
}

func TestNoticeMarkupHasCloseButtonAndTimer(t *testing.T) {
	var buf bytes.Buffer
	data := pageData{Info: requestContext{User: User{ID: 1, Role: "admin"}, Path: "/images"}, Message: "ISO удалён", Error: "Ошибка"}
	for _, name := range []string{"top", "bottom"} {
		if err := pagesByLanguage[langRU].ExecuteTemplate(&buf, name, data); err != nil {
			t.Fatal(err)
		}
	}
	html := buf.String()
	for _, want := range []string{
		`<div class="notice" role="status" data-notice data-notice-auto>`,
		`<div class="notice error" role="alert" data-notice>`,
		`data-notice-close aria-label="Закрыть"`,
		`<span class="notice-timer" aria-hidden="true"></span>`,
		`<script defer src="/assets/notice.js"></script>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("layout does not contain %q", want)
		}
	}
	if strings.Count(html, "notice-timer") != 1 {
		t.Errorf("only the regular message should auto-hide")
	}
}
