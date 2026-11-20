package control

import (
	"bytes"
	"strings"
	"testing"
)

func TestFormatNavBadgesEmpty(t *testing.T) {
	if got := formatNavBadges(navBadgeSnapshot{}, "ru"); got != nil {
		t.Fatalf("expected nil badges for empty snapshot, got %+v", got)
	}
}

func TestFormatNavBadgesPriorityAndLocalization(t *testing.T) {
	snap := navBadgeSnapshot{
		sysErrors:      2,
		storageDown:    1,
		recoveryError:  true,
		storageMaint:   1,
		imageMaint:     1,
		imageMigrating: 2,
		winpeError:     1,
		winpePreparing: 1,
		winpePending:   2,
	}

	ru := formatNavBadges(snap, "ru")
	if b := ru["/events"]; b.Level != "bad" || b.Count != 2 || !strings.Contains(b.Hint, "Активных системных сбоев: 2") {
		t.Fatalf("unexpected /events RU badge: %+v", b)
	}
	if b := ru["/storages"]; b.Level != "bad" || b.Count != 2 || !strings.Contains(b.Hint, "Недоступно хранилищ: 1") || !strings.Contains(b.Hint, "Ошибка автовосстановления хранилищ") || !strings.Contains(b.Hint, "Хранилищ на обслуживании: 1") {
		t.Fatalf("unexpected /storages RU badge: %+v", b)
	}
	if b := ru["/images"]; b.Level != "accent" || b.Count != 3 || !strings.Contains(b.Hint, "Образов на обслуживании: 1") || !strings.Contains(b.Hint, "Переносится образов: 2") {
		t.Fatalf("unexpected /images RU badge: %+v", b)
	}
	if b := ru["/installations"]; b.Level != "bad" || b.Count != 1 || !strings.Contains(b.Hint, "Ошибок сборки WinPE: 1") || !strings.Contains(b.Hint, "Собирается WinPE: 1") || !strings.Contains(b.Hint, "Ожидает сборки WinPE: 2") {
		t.Fatalf("unexpected /installations RU badge: %+v", b)
	}

	en := formatNavBadges(snap, "en")
	if b := en["/events"]; b.Level != "bad" || b.Count != 2 || !strings.Contains(b.Hint, "Active system errors: 2") {
		t.Fatalf("unexpected /events EN badge: %+v", b)
	}
	if b := en["/images"]; b.Level != "accent" || b.Count != 3 || !strings.Contains(b.Hint, "Images migrating: 2") {
		t.Fatalf("unexpected /images EN badge: %+v", b)
	}
}

func TestNavigationWithBadgesAggregationAndRoles(t *testing.T) {
	badges := map[string]navBadge{
		"/events":        {Level: "bad", Count: 2, Hint: "Активных системных сбоев: 2"},
		"/installations": {Level: "accent", Count: 1, Hint: "Собирается WinPE: 1"},
		"/images":        {Level: "bad", Count: 1, Hint: "Недоступно образов: 1"},
		"/storages":      {Level: "accent", Count: 1, Hint: "Хранилищ на обслуживании: 1"},
		"/users":         {Level: "bad", Count: 9, Hint: "admin-only"},
	}

	// Observer does not see /users, so Система should only reflect /storages (accent).
	observerGroups := navigationWithBadges("observer", "/images", badges)
	byTitle := map[string]navGroup{}
	for _, g := range observerGroups {
		byTitle[g.Title] = g
	}

	if b := byTitle["Сейчас"].Badge; b.Level != "" || b.Count != 0 {
		t.Fatalf("expected no badge on Сейчас, got %+v", b)
	}
	if b := byTitle["Журнал"].Badge; b.Level != "bad" || b.Count != 2 {
		t.Fatalf("expected bad(2) on Журнал, got %+v", b)
	}
	if b := byTitle["Библиотека"].Badge; b.Level != "bad" || b.Count != 1 || !strings.Contains(b.Hint, "Недоступно образов: 1") || !strings.Contains(b.Hint, "Собирается WinPE: 1") {
		t.Fatalf("expected bad(1) with combined hints on Библиотека, got %+v", b)
	}
	if b := byTitle["Система"].Badge; b.Level != "accent" || b.Count != 1 {
		t.Fatalf("observer should not aggregate admin-only /users badge into Система, got %+v", b)
	}
}

func TestLayoutRendersNavBadges(t *testing.T) {
	badges := formatNavBadges(navBadgeSnapshot{
		sysErrors:      2,
		storageDown:    1,
		winpePreparing: 1,
	}, "ru")

	data := pageData{
		Info: requestContext{
			User:      User{ID: 1, Username: "admin", Role: "admin", Language: "ru"},
			Path:      "/installations",
			NavBadges: badges,
		},
	}

	var buf bytes.Buffer
	if err := pagesByLanguage[langRU].ExecuteTemplate(&buf, "top", data); err != nil {
		t.Fatalf("render top template: %v", err)
	}
	html := buf.String()

	for _, want := range []string{
		`id="topnav"`,
		`id="subnav"`,
		`class="nav-dot nav-dot-accent" role="img" title="Библиотека: Собирается WinPE: 1"`,
		`class="nav-dot nav-dot-bad" role="img" title="Журнал: Активных системных сбоев: 2"`,
		`class="nav-dot nav-dot-bad" role="img" title="Система: Недоступно хранилищ: 1"`,
		`class="nav-badge nav-badge-accent mono" title="Загрузки: Собирается WinPE: 1" aria-label="Загрузки: Собирается WinPE: 1">1</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected rendered layout to contain %q\nHTML:\n%s", want, html)
		}
	}

	// EN rendering check.
	data.Info.User.Language = "en"
	data.Info.NavBadges = formatNavBadges(navBadgeSnapshot{sysErrors: 1}, "en")
	buf.Reset()
	if err := pagesByLanguage[langEN].ExecuteTemplate(&buf, "top", data); err != nil {
		t.Fatalf("render EN top template: %v", err)
	}
	if !strings.Contains(buf.String(), `title="Log: Active system errors: 1"`) {
		t.Fatalf("expected EN nav dot title, got:\n%s", buf.String())
	}

	// Clean state renders no dots or badges.
	data.Info.User.Language = "ru"
	data.Info.NavBadges = nil
	buf.Reset()
	if err := pagesByLanguage[langRU].ExecuteTemplate(&buf, "top", data); err != nil {
		t.Fatalf("render clean top template: %v", err)
	}
	cleanHTML := buf.String()
	if strings.Contains(cleanHTML, "nav-dot") || strings.Contains(cleanHTML, "nav-badge") {
		t.Fatalf("expected no nav badges in clean state, got:\n%s", cleanHTML)
	}
}

func TestNavBadgeCacheReadWrite(t *testing.T) {
	var cache navBadgeCache
	if _, ok := cache.get(); ok {
		t.Fatalf("empty cache should miss")
	}
	cache.set(navBadgeSnapshot{sysErrors: 3})
	if got, ok := cache.get(); !ok || got.sysErrors != 3 {
		t.Fatalf("expected cache hit, got ok=%v snap=%+v", ok, got)
	}
	s := &Server{}
	if got := s.navBadges("ru"); got != nil {
		t.Fatalf("expected nil badges before background refresh, got %+v", got)
	}
	s.navBadgesCache.set(navBadgeSnapshot{sysErrors: 2})
	if got := s.navBadges("ru"); got["/events"].Count != 2 || got["/events"].Level != "bad" {
		t.Fatalf("expected cached /events badge, got %+v", got)
	}
}
