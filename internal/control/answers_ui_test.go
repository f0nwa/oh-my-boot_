package control

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestAnswersScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/answers.js", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("unexpected Content-Type: %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"answer-upload-dialog", "data-answer-upload-open", "data-close-dialog", "showModal", "data-answers-search"} {
		if !strings.Contains(body, want) {
			t.Fatalf("answers.js lacks %q", want)
		}
	}
}

func TestAnswersPageLayout(t *testing.T) {
	s := libraryServer()
	body := libraryGet(t, s, "/answers")

	for _, want := range []string{
		`data-row-menu`,
		`popovertarget="answers-add-menu"`,
		`id="answers-add-menu"`,
		`class="row-menu-popover"`,
		`href="/answers/editor"`,
		`data-answer-upload-open`,
		`href="/ubuntu-autoinstall/new"`,
		`id="answer-upload-dialog"`,
		`class="user-dialog"`,
		`data-close-dialog`,
		`class="autoinstall-section"`,
		`id="ubuntu-autoinstall"`,
		`src="/assets/answers.js"`,
		`action="/answers/upload"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/answers missing %q in body:\n%s", want, body)
		}
	}

	// Verify <details> is no longer used for "Загрузить XML" or "Проверить"
	if strings.Contains(body, `<details><summary>Загрузить XML</summary>`) {
		t.Fatalf("/answers still contains <details> for upload XML")
	}
	if strings.Contains(body, `<th>Диск</th>`) {
		t.Fatalf("/answers should not contain separate Disk table header")
	}
	if strings.Contains(body, `Проверить`) {
		t.Fatalf("/answers should not contain inspect/verify button in table")
	}
	if strings.Contains(body, `name="profile_id"`) {
		t.Fatalf("/answers upload dialog should not contain profile_id selector")
	}
}

func TestAnswersPageDiskWarning(t *testing.T) {
	for _, tc := range []struct {
		lang      string
		wantBadge string
	}{
		{langRU, `<span class="danger-text">Изменяет разделы</span>`},
		{langEN, `<span class="danger-text">Changes partitions</span>`},
	} {
		body := renderPage("answers", samplePageData(tc.lang))
		if !strings.Contains(body, tc.wantBadge) {
			t.Fatalf("expected danger badge %q for lang %s, got:\n%s", tc.wantBadge, tc.lang, body)
		}
	}
}

func TestAnswersPageObserverOmitsActions(t *testing.T) {
	s := libraryServer()
	s.Store = authOnlyStore{user: User{ID: 2, Role: "observer", Enabled: true}}
	body := libraryGet(t, s, "/answers")

	if strings.Contains(body, "answers-add-menu") {
		t.Fatalf("observer should not see add menu")
	}
	if strings.Contains(body, "data-answer-upload-open") {
		t.Fatalf("observer should not see upload trigger")
	}
	if strings.Contains(body, "id=\"answer-upload-dialog\"") {
		t.Fatalf("observer should not see upload dialog")
	}
	if strings.Contains(body, `href="/ubuntu-autoinstall/new"`) {
		t.Fatalf("observer should not see create ubuntu button")
	}
}

func TestAnswerRowActions(t *testing.T) {
	for _, role := range []string{"admin", "operator", "observer"} {
		data := samplePageData(langRU)
		data.Info.User.Role = role
		data.AnswerFileUsage.DownloadsKnown = true
		body := renderPage("answers", data)
		for _, want := range []string{`popovertarget="answer-menu-5"`, `id="answer-menu-5"`, `href="/answers/5/edit"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %s", role, want)
			}
		}
		hasDelete := strings.Contains(body, `action="/images/windows-answers/5/delete"`)
		if hasDelete != (role == "admin") {
			t.Errorf("%s: unexpected delete availability", role)
		}
		if strings.Contains(body, `<summary>Удалить сценарий #`) {
			t.Errorf("%s: obsolete disclosure", role)
		}
	}
}

func TestAnswerFilesCommonColumns(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		data := samplePageData(lang)
		data.UbuntuAutoinstalls = []UbuntuAutoinstallSummary{{ID: 41, Title: "Ubuntu shared", State: "validated", DiskMode: "interactive", Author: "ubuntu-creator", CreatedAt: time.Date(2026, 11, 2, 8, 15, 0, 0, time.UTC)}}
		data.WindowsAnswerVariants[0].CreatedAt = time.Date(2026, 11, 1, 7, 30, 0, 0, time.UTC)
		body := renderPage("answers", data)
		tables := regexp.MustCompile(`(?s)<table class="answers-table"[^>]*>(.*?)</table>`).FindAllStringSubmatch(body, -1)
		if len(tables) != 2 {
			t.Fatalf("%s: got %d answer tables, want two", lang, len(tables))
		}
		var headers []string
		for _, table := range tables {
			head := regexp.MustCompile(`(?s)<thead>(.*?)</thead>`).FindStringSubmatch(table[1])
			if len(head) != 2 {
				t.Fatal("missing semantic header")
			}
			cols := regexp.MustCompile(`<th(?:\s[^>]*)?>`).FindAllString(head[1], -1)
			if len(cols) != 10 {
				t.Fatalf("got %d columns, want 10", len(cols))
			}
			headers = append(headers, head[1])
		}
		if headers[0] != headers[1] {
			t.Fatal("headers differ between operating systems")
		}
		for _, want := range []string{">Windows</h2>", ">Ubuntu</h2>", "ubuntu-creator", "2026-11-02", "2026-11-01", `popovertarget="ubuntu-answer-menu-41"`, `href="/ubuntu-autoinstall/41/download"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing %q", lang, want)
			}
		}
		if strings.Contains(tables[1][1], ">Проверен<") || strings.Contains(tables[1][1], ">Validated<") {
			t.Fatal("Ubuntu uses different ready terminology")
		}
	}
}

func TestAnswerDeleteAvailability(t *testing.T) {
	for _, tc := range []struct {
		name      string
		known     bool
		downloads []answerFileDownload
		disabled  bool
		reason    string
	}{
		{name: "free", known: true},
		{name: "used", known: true, downloads: []answerFileDownload{{ID: 9, Title: "Boot"}}, disabled: true, reason: "Сценарий используется загрузками"},
		{name: "unknown", disabled: true, reason: "Не удалось проверить использование"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := samplePageData(langRU)
			data.AnswerFileUsage = answerFileUsage{DownloadsKnown: tc.known, MenuKnown: true, Windows: map[int64]answerFileUses{5: {Downloads: tc.downloads}}}
			body := renderPage("answers", data)
			match := regexp.MustCompile(`<button[^>]*data-answer-delete-open="answer-delete-5"[^>]*>[^<]*</button>`).FindString(body)
			if match == "" || strings.Contains(match, " disabled") != tc.disabled || (tc.reason != "" && !strings.Contains(match, tc.reason)) {
				t.Fatalf("unexpected button: %s", match)
			}
			if !strings.Contains(match, ">Удалить сценарий</button>") {
				t.Fatalf("unexpected label: %s", match)
			}
			hasDialog := strings.Contains(body, `id="answer-delete-5"`)
			if hasDialog == tc.disabled {
				t.Fatalf("dialog present=%v, disabled=%v", hasDialog, tc.disabled)
			}
		})
	}
}

func TestAnswersPageSearchByAuthorAndSHA(t *testing.T) {
	s := libraryServer()

	s.authorsCache.set(ResourceAuthors{
		Answers:       map[int64]string{5: "vlad_author"},
		UbuntuAnswers: map[int64]string{7: "ivan_author"},
	}, time.Now())

	body := libraryGet(t, s, "/answers?q=vlad_author")
	if !strings.Contains(body, "data-answers-search") {
		t.Fatalf("expected answers search toolbar in response, got: %s", body)
	}
	if !strings.Contains(body, "vlad_author") {
		t.Fatalf("expected search by author to find vlad_author in answers, got: %s", body)
	}

	emptyBody := libraryGet(t, s, "/answers?q=nonexistent_query_xyz")
	if strings.Contains(emptyBody, "vlad_author") {
		t.Fatalf("expected non-matching search to exclude vlad_author")
	}
}

func TestAnswersPageUnboundTargetsAndPerImageChecks(t *testing.T) {
	data := samplePageData(langRU)
	data.AnswerChecksKnown = true
	data.WindowsAnswerVariants = []WindowsAnswerVariant{{
		ID:     5,
		Title:  "Universal Pro",
		State:  "ready",
		SHA256: strings.Repeat("a", 64),
		Target: WindowsAnswerTarget{
			Architectures: []string{"amd64"},
			ImageMode:     "edition",
			EditionID:     "pro",
			ImageLanguage: "ru-RU",
			Win11Only:     true,
		},
	}}
	data.UbuntuAutoinstalls = []UbuntuAutoinstallSummary{{ID: 41, Title: "Ubuntu shared", State: "validated", DiskMode: "interactive", SHA256: strings.Repeat("b", 64)}}
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	data.WindowsProfileChecks = map[int64][]AnswerCheck{
		5: {
			{ID: 1, Kind: "windows", AnswerID: 5, WindowsProfileID: 11, ProfileTitle: "Win 11 24H2", SHA256: strings.Repeat("a", 64), Result: "success", Author: "op", CreatedAt: now},
			{ID: 2, Kind: "windows", AnswerID: 5, WindowsProfileID: 12, ProfileTitle: "Win 11 23H2", SHA256: strings.Repeat("a", 64), Result: "success", Author: "op", CreatedAt: now.Add(-time.Hour)},
			{ID: 3, Kind: "windows", AnswerID: 5, WindowsProfileID: 13, ProfileTitle: "Win 10 22H2", SHA256: strings.Repeat("a", 64), Result: "failure", Author: "op", CreatedAt: now.Add(-2 * time.Hour)},
			{ID: 4, Kind: "windows", AnswerID: 5, WindowsProfileID: 14, ProfileTitle: "Win 10 LTSC", SHA256: strings.Repeat("a", 64), Result: "success", Author: "op", CreatedAt: now.Add(-3 * time.Hour)},
		},
	}

	body := renderPage("answers", data)
	for _, want := range []string{
		`>Рассчитан на</a></th>`,
		`>Проверки по образам</th>`,
		`amd64`,
		`Win 11`,
		`Pro`,
		`ru-RU`,
		`Ubuntu Desktop`,
		`Win 11 24H2`,
		`Win 11 23H2`,
		`Win 10 22H2`,
		`ещё 1`,
		`name="windows_profile_id"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/answers missing %q in:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{`>Образ</th>`, `>Проверка установки</th>`, `Без привязки к ISO`, `Win 10 LTSC`, `name="profile_id"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("/answers contains forbidden %q", forbidden)
		}
	}

	ed, _, _ := editorServer("operator", langRU)
	edPage := editorGet(ed, "/answers/editor")
	if edPage.Code != http.StatusOK || strings.Contains(edPage.Body.String(), `name="profile_id"`) {
		t.Fatalf("/answers/editor status=%d or still has profile_id", edPage.Code)
	}
}
