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

func TestUbuntuEditorRegionalFieldsOfferCommonChoices(t *testing.T) {
	s, _ := autoinstallServer(t)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/ubuntu-autoinstall/new", nil))
	body := res.Body.String()
	for _, want := range []string{`list="ai-locale-options"`, `<option value="ru_RU.UTF-8">`, `list="ai-timezone-options"`, `<option value="Europe/Moscow">`, `placeholder="192.0.2.20/24"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("regional choice %q missing: %s", want, body)
		}
	}
	if strings.Contains(body, `placeholder="192.168.`) {
		t.Fatalf("autoinstall editor contains local IP placeholder: %s", body)
	}
}

func TestUbuntuEditorPasswordFeedbackExplainsClearedField(t *testing.T) {
	s, _ := autoinstallServer(t)
	res := constructorPost(t, s, "/ubuntu-autoinstall/preview", constructorForm())
	body := res.Body.String()
	for _, want := range []string{"Пароль создаваемого пользователя", "Пароль принят. В предпросмотре сохранён только хеш, поле очищено."} {
		if !strings.Contains(body, want) {
			t.Fatalf("password feedback %q missing: %s", want, body)
		}
	}
	if strings.Contains(body, constructorPassword) {
		t.Fatal("password feedback leaked plaintext")
	}
}

func TestUbuntuEditorShutdownHasOneExplicitRebootChoice(t *testing.T) {
	s, _ := autoinstallServer(t)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/ubuntu-autoinstall/new", nil))
	body := res.Body.String()
	if count := strings.Count(body, ">Перезагрузить компьютер</option>"); count != 1 {
		t.Fatalf("explicit reboot choices=%d, want 1: %s", count, body)
	}
	if !strings.Contains(body, "Как в Ubuntu — перезагрузка") {
		t.Fatal("default shutdown behavior is unclear")
	}
}

func TestUbuntuEditorOffersDefaultDiskWithWarning(t *testing.T) {
	s, _ := autoinstallServer(t)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/ubuntu-autoinstall/new", nil))
	body := res.Body.String()
	for _, want := range []string{`value="default"`, "Стереть диск по умолчанию", "Установщик сам выберет один диск и полностью сотрёт его содержимое."} {
		if !strings.Contains(body, want) {
			t.Fatalf("default disk choice or warning %q missing: %s", want, body)
		}
	}
}

func TestUbuntuEditorDefaultDiskSummaryWarnsAboutInstallerChoice(t *testing.T) {
	s, _ := autoinstallServer(t)
	form := constructorForm()
	form.Set("disk_mode", "default")
	res := constructorPost(t, s, "/ubuntu-autoinstall/preview", form)
	body := res.Body.String()
	for _, want := range []string{"Диск по умолчанию (выбор установщика)", "Автоматическая разметка стирает диск, выбранный установщиком. Проверьте целевой диск перед публикацией."} {
		if !strings.Contains(body, want) {
			t.Fatalf("default disk summary or warning %q missing: %s", want, body)
		}
	}
}
