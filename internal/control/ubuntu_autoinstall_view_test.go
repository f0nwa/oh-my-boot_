package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"
)

func TestUbuntuEditorPreservesInputWithoutPassword(t *testing.T) {
	s, _ := autoinstallServer(t)
	f := constructorForm()
	f.Set("timezone", "Unknown/Zone")
	res := constructorPost(t, s, "/ubuntu-autoinstall/preview", f)
	body := res.Body.String()
	if res.Code != 400 || !strings.Contains(body, `name="hostname" value="lab-ubuntu"`) {
		t.Fatalf("lost form %d %s", res.Code, body)
	}
	if strings.Contains(body, constructorPassword) || !strings.Contains(body, `role="alert"`) {
		t.Fatal("unsafe or missing diagnostic")
	}
}
func TestUbuntuEditorNavigationDoesNotValidate(t *testing.T) {
	s, _ := autoinstallServer(t)
	f := constructorForm()
	f.Set("username", "")
	f.Set("section", "network")
	f.Set("realname", "Example")
	res := constructorPost(t, s, "/ubuntu-autoinstall/preview", f)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `name="realname" value="Example"`) || strings.Contains(res.Body.String(), constructorPassword) {
		t.Fatalf("navigation failed %d", res.Code)
	}
}
func TestUbuntuEditorEnglishAndEscaping(t *testing.T) {
	s, store := autoinstallServer(t)
	store.user.Language = "en"
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/ubuntu-autoinstall/new", nil))
	body := strings.ReplaceAll(res.Body.String(), "Русский", "")
	if res.Code != 200 {
		t.Fatal(res.Code)
	}
	for _, r := range body {
		if unicode.In(r, unicode.Cyrillic) {
			t.Fatalf("untranslated UI: %s", body)
		}
	}
	f := constructorForm()
	f.Set("realname", "<script>alert(1)</script>")
	r := constructorPost(t, s, "/ubuntu-autoinstall/preview", f)
	if strings.Contains(r.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("unescaped input")
	}
}
