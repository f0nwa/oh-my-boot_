package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUbuntuPreviewJSONMatchesHTML(t *testing.T) {
	s, _ := autoinstallServer(t)
	f := constructorForm()
	token, data := constructorPreview(t, s, f)
	f.Set("preview_token", token)
	f.Set("password", "")
	req := accountRequest(http.MethodPost, "/ubuntu-autoinstall/preview", f)
	req.Header.Set("Accept", "application/json")
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	var result struct {
		Token, YAML string
		Ready       bool
	}
	if json.Unmarshal(res.Body.Bytes(), &result) != nil || res.Code != 200 || result.YAML != string(data) || !result.Ready || result.Token == "" {
		t.Fatalf("invalid JSON preview %d %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), constructorPassword) {
		t.Fatal("password leak")
	}
	f.Set("timezone", "invalid")
	req = accountRequest(http.MethodPost, "/ubuntu-autoinstall/preview", f)
	req.Header.Set("Accept", "application/json")
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != 400 || !strings.Contains(res.Header().Get("Content-Type"), "application/json") {
		t.Fatal("JSON field errors not returned")
	}
	if json.Unmarshal(res.Body.Bytes(), &result) != nil || result.Ready || result.YAML != "" {
		t.Fatal("stale success returned for invalid input")
	}
}
func TestUbuntuEditorScriptServed(t *testing.T) {
	s, _ := autoinstallServer(t)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/assets/ubuntu_autoinstall_editor.js", nil))
	if res.Code != 200 || !strings.Contains(res.Header().Get("Content-Type"), "javascript") || !strings.Contains(res.Body.String(), "AbortController") {
		t.Fatalf("missing script: %d", res.Code)
	}
}
