package control

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShaChipScriptEndpoint(t *testing.T) {
	req := httptest.NewRequest("GET", "/assets/sha-chip.js", nil)
	rec := httptest.NewRecorder()

	shaChipScript(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/javascript") {
		t.Errorf("expected text/javascript content type, got %s", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data-sha-chip") || !strings.Contains(body, "data-sha-copy") {
		t.Errorf("expected sha_chip.js to handle data-sha-chip and data-sha-copy, got: %s", body)
	}
}

func TestShaChipTemplateRender(t *testing.T) {
	tmpl := pagesByLanguage[langRU]
	if tmpl == nil {
		t.Fatal("pagesByLanguage not initialized")
	}

	var buf bytes.Buffer
	sha := "5eecca2780bd49c900565e124516a1bd666ec5e012825f34991b6ba1ef2fa6cf"
	if err := tmpl.ExecuteTemplate(&buf, "sha_chip", sha); err != nil {
		t.Fatalf("failed to execute sha_chip template: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, `class="sha-chip"`) {
		t.Errorf("expected class sha-chip, got: %s", out)
	}
	if !strings.Contains(out, `data-sha-chip`) {
		t.Errorf("expected data-sha-chip attribute, got: %s", out)
	}
	if !strings.Contains(out, `data-short="5eec…a6cf"`) {
		t.Errorf("expected short sha 5eec…a6cf, got: %s", out)
	}
	if !strings.Contains(out, `data-full="`+sha+`"`) {
		t.Errorf("expected full sha attribute, got: %s", out)
	}
	if !strings.Contains(out, `data-sha-copy`) {
		t.Errorf("expected data-sha-copy button, got: %s", out)
	}
}
