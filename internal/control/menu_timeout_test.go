package control

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestLocalDiskTimerSecondsAndOff(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	base := MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}}}
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
		script, err := BuildMenuFromDraft(c.draft, testBootURL, store.profiles, store.answers, store.configs)
		if err != nil || !strings.Contains(string(script), c.want) {
			t.Fatalf("err=%v want %q in %s", err, c.want, script)
		}
		back := draftFromVersion(MenuVersion{Script: string(script)})
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

func TestMenuSettingsSetLocalDiskTimer(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready"}
	s, _ := draftServer(t, store)
	form := url.Values{"label": {"Diagnostics"}, "message": {"Ready"}, "local_timer": {"on"}, "local_timeout": {"30"}}
	if res := menuPost(t, s, "/menu/settings", form); res.Code != http.StatusSeeOther || !store.draft.LocalTimerOn() || store.draft.LocalTimeoutSeconds() != 30 {
		t.Fatalf("status=%d draft=%+v", res.Code, store.draft)
	}
	form.Del("local_timer")
	if res := menuPost(t, s, "/menu/settings", form); res.Code != http.StatusSeeOther || store.draft.LocalTimerOn() {
		t.Fatalf("timer not turned off: status=%d draft=%+v", res.Code, store.draft)
	}
	for _, bad := range []string{"0", "3601", "x", ""} {
		form.Set("local_timer", "on")
		form.Set("local_timeout", bad)
		if res := menuPost(t, s, "/menu/settings", form); res.Code == http.StatusSeeOther {
			t.Fatalf("accepted %q seconds", bad)
		}
	}
}

func TestLocalDiskTimerChangeIsPending(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Ready", NoLocalTimer: true, Items: []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}}}
	s, _ := draftServer(t, store)
	draft, active, err := s.currentDraft(t.Context())
	if err != nil || len(diffMenuDraft(active, draft)) == 0 {
		t.Fatalf("err=%v: turning the timer off is not a pending change", err)
	}
}
