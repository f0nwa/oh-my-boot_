package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestLocalDiskTimerSecondsAndOff(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	base := MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}}}
	for _, c := range []struct {
		draft MenuDraft
		want  string
		hint  string
		secs  int
		on    bool
	}{
		{base, "choose --default local --timeout 10000 target || goto local\n", "(10)", 10, true},
		{func() MenuDraft { d := base; d.LocalTimeout = 45; return d }(), "choose --default local --timeout 45000 target || goto local\n", "(45)", 45, true},
		{func() MenuDraft { d := base; d.LocalTimeout = 45; d.NoLocalTimer = true; return d }(), "choose --default local target || goto local\n", "", 45, false},
	} {
		script, err := buildTestMenuFromDraft(c.draft, testBootURL, store.profiles, store.answers, store.configs)
		if err != nil || !strings.Contains(string(script), c.want) {
			t.Fatalf("err=%v want %q in %s", err, c.want, script)
		}
		back := draftFromVersion(MenuVersion{Script: string(script), LocalTimeout: c.draft.LocalTimeout, NoLocalTimer: c.draft.NoLocalTimer, Items: versionItems(c.draft)})
		if back.LocalTimerOn() != c.on || (c.on && back.LocalTimeoutSeconds() != c.secs) {
			t.Fatalf("read back on=%v secs=%d, want %v %d", back.LocalTimerOn(), back.LocalTimeoutSeconds(), c.on, c.secs)
		}
		if hint := localDiskHint(string(script)); hint != c.hint {
			t.Fatalf("preview hint %q, want %q", hint, c.hint)
		}
	}
	// A version published before the setting existed reads as 10 seconds.
	if d := draftFromVersion(MenuVersion{Script: "#!ipxe\nchoose --default local --timeout 10000 target || goto local\n"}); !d.LocalTimerOn() || d.LocalTimeoutSeconds() != 10 {
		t.Fatalf("old version: %+v", d)
	}
}

func TestLocalDiskTimerIsSetOnItsMenuItem(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready"}
	s, _ := draftServer(t, store)
	form := url.Values{"local_timer": {"on"}, "local_timeout": {"30"}}
	res := menuPost(t, s, "/menu/local-timer", form)
	if res.Code != http.StatusSeeOther || !strings.Contains(res.Header().Get("Location"), "item=local") || !store.draft.LocalTimerOn() || store.draft.LocalTimeoutSeconds() != 30 {
		t.Fatalf("status=%d location=%q draft=%+v", res.Code, res.Header().Get("Location"), store.draft)
	}
	form.Del("local_timer")
	if res := menuPost(t, s, "/menu/local-timer", form); res.Code != http.StatusSeeOther || store.draft.LocalTimerOn() || store.draft.LocalTimeoutSeconds() != 30 {
		t.Fatalf("timer not turned off: status=%d draft=%+v", res.Code, store.draft)
	}
	for _, bad := range []string{"0", "3601", "x", ""} {
		form.Set("local_timer", "on")
		form.Set("local_timeout", bad)
		if res := menuPost(t, s, "/menu/local-timer", form); res.Code == http.StatusSeeOther {
			t.Fatalf("accepted %q seconds", bad)
		}
	}
	// Saving the other menu settings leaves the timer as it is.
	store.draft.NoLocalTimer, store.draft.LocalTimeout = true, 45
	if res := menuPost(t, s, "/menu/diagnostics", url.Values{"label": {"Diagnostics"}, "message": {"Ready"}}); res.Code != http.StatusSeeOther || store.draft.LocalTimerOn() || store.draft.LocalTimeout != 45 {
		t.Fatalf("menu settings changed the timer: status=%d draft=%+v", res.Code, store.draft)
	}
}

func TestLocalDiskItemShowsTimerAndGreysOutWhenOff(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", LocalTimeout: 25, Items: []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}}}
	s, _ := draftServer(t, store)
	get := func() string {
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/menu?item=local", nil))
		return res.Body.String()
	}
	localTreeLine := func(body string) string {
		end := strings.Index(body, `href="/menu?item=local"`)
		if end < 0 {
			t.Fatal("local disk is missing from the structure")
		}
		return body[strings.LastIndex(body[:end], `<div class="tree-line`):end]
	}
	body := get()
	if !strings.Contains(body, `action="/menu/local-timer"`) || !strings.Contains(body, `name="local_timeout"`) || !strings.Contains(body, `value="25"`) || strings.Contains(localTreeLine(body), "tree-off") {
		t.Fatal("the local disk item has no timer settings or is greyed out while on")
	}
	if strings.Contains(body, `action="/menu/settings"`) {
		t.Fatal("Ubuntu settings are shown with the local disk item")
	}
	store.draft.NoLocalTimer = true
	if body := get(); !strings.Contains(localTreeLine(body), "tree-off") {
		t.Fatal("the local disk line is not greyed out with the timer off")
	}
}

func TestLocalDiskTimerChangeIsPending(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", NoLocalTimer: true, Items: []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}}}
	s, _ := draftServer(t, store)
	draft, active, err := s.currentDraft(t.Context())
	if err != nil || len(diffMenuDraft(active, draft)) == 0 {
		t.Fatalf("err=%v: turning the timer off is not a pending change", err)
	}
}

func TestLocalDiskHintUsesCorrectUnits(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		for _, seconds := range []int{1, 2, 5, 21} {
			data := samplePageData(lang)
			data.Draft.LocalTimeout = seconds
			res := httptest.NewRecorder()
			render(res, "menu", data)
			body := res.Body.String()
			want := "через " + strconv.Itoa(seconds) + " с загружает локальный диск"
			if lang == langEN {
				want = "waits " + strconv.Itoa(seconds) + " s before booting from the local disk"
			}
			if res.Code != http.StatusOK || !strings.Contains(body, want) {
				t.Fatalf("lang=%s seconds=%d: countdown unit missing, status=%d", lang, seconds, res.Code)
			}
		}
	}
}

func TestTimerCanBootInstallationInFolder(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11", Folder: "Windows"}})
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11", Folder: "Windows"}}}
	s, _ := draftServer(t, store)
	form := url.Values{"local_timer": {"on"}, "local_timeout": {"30"}, "default_installation_id": {"1017"}}
	if res := menuPost(t, s, "/menu/local-timer", form); res.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", res.Code)
	}
	script, err := buildTestMenuFromDraft(*store.draft, testBootURL, store.profiles, store.answers, store.configs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "item install_1017 Windows 11") || !strings.Contains(string(script), "choose --default install_1017 --timeout 30000 target") {
		t.Fatalf("timer does not select installation: %s", script)
	}
}

func TestTimerSelectionRemovedWithItem(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", DefaultInstallationID: 1017, Items: []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11"}}}
	s, _ := draftServer(t, store)
	res := menuPost(t, s, "/menu/items/install_1017/remove", url.Values{})
	if res.Code != http.StatusSeeOther || store.draft.DefaultInstallationID != 0 {
		t.Fatalf("status=%d draft=%+v", res.Code, store.draft)
	}
}

func TestTimerSettingsShowSelectedInstallation(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11", Folder: "Windows"}})
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", DefaultInstallationID: 1017, Items: []MenuDraftItem{{InstallationID: 1017, Title: "Windows 11", Folder: "Windows"}}}
	s, _ := draftServer(t, store)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/menu?item=local", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `value="1017" selected`) {
		t.Fatalf("settings status=%d: %s", res.Code, res.Body.String())
	}
}
