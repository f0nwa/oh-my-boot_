package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func machinesFixture() *bookStore {
	t0 := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	return &bookStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}, machines: map[string]MachineInfo{
		"bc:24:11:52:02:37": {MAC: "bc:24:11:52:02:37", FirstSeen: t0, LastSeen: t0.Add(72 * time.Hour), LastIP: "10.30.0.17", Firmware: "UEFI",
			Manufacturer: "QEMU", Product: "Standard PC (Q35 + ICH9, 2009)", Serial: "SN-VM17", Name: "test-vm-17", Note: "стенд для проверки установки Windows 11 с драйверами и ответами"},
		"00:1b:21:aa:bb:cc": {MAC: "00:1b:21:aa:bb:cc", FirstSeen: t0.Add(time.Hour), LastSeen: t0.Add(2 * time.Hour), LastIP: "10.30.0.40", Firmware: "BIOS"},
		"3c:52:82:01:02:03": {MAC: "3c:52:82:01:02:03", FirstSeen: t0.Add(30 * time.Minute), LastSeen: t0.Add(24 * time.Hour), LastIP: "10.30.0.33", Firmware: "UEFI",
			Manufacturer: "HP", Product: "EliteBook 840 G8", Serial: "5CG1234XYZ"},
	}}
}

// rowOrder returns the MACs in the order the page lists them.
func rowOrder(page string, macs ...string) []string {
	type pos struct {
		mac string
		at  int
	}
	var found []pos
	for _, mac := range macs {
		if i := strings.Index(page, `href="/events/machines/`+mac+`"`); i >= 0 {
			found = append(found, pos{mac, i})
		}
	}
	for i := range found {
		for j := i + 1; j < len(found); j++ {
			if found[j].at < found[i].at {
				found[i], found[j] = found[j], found[i]
			}
		}
	}
	out := make([]string, len(found))
	for i, f := range found {
		out[i] = f.mac
	}
	return out
}

func TestMachinesPageListsTheBook(t *testing.T) {
	s := &Server{Store: machinesFixture()}
	page := libraryGet(t, s, "/machines")
	for _, want := range []string{
		"<h1>Машины</h1>",
		"test-vm-17", "SN-VM17", "10.30.0.17",
		"HP EliteBook 840 G8", "5CG1234XYZ",
		"Intel Corporate", // NIC vendor of 00:1b:21 when the model is unknown
		`title="стенд для проверки установки Windows 11 с драйверами и ответами"`,
		"Всего: 3", "с именем: 1", "без модели: 1",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %q:\n%s", want, page)
		}
	}
	all := []string{"bc:24:11:52:02:37", "00:1b:21:aa:bb:cc", "3c:52:82:01:02:03"}
	if got := strings.Join(rowOrder(page, all...), " "); got != "bc:24:11:52:02:37 3c:52:82:01:02:03 00:1b:21:aa:bb:cc" {
		t.Fatalf("default order (last seen, newest first): %s", got)
	}
	// The menu shows the page under «Система».
	if !strings.Contains(page, `href="/machines"`) {
		t.Fatal("no nav link")
	}
}

func TestMachinesPageSearchesAndSorts(t *testing.T) {
	s := &Server{Store: machinesFixture()}
	all := []string{"bc:24:11:52:02:37", "00:1b:21:aa:bb:cc", "3c:52:82:01:02:03"}
	for query, want := range map[string]string{
		"elitebook":   "3c:52:82:01:02:03",
		"5cg1234":     "3c:52:82:01:02:03",
		"СТЕНД":       "bc:24:11:52:02:37",
		"001b21":      "00:1b:21:aa:bb:cc", // MAC typed without separators
		"00-1B-21-AA": "00:1b:21:aa:bb:cc",
		"10.30.0.33":  "3c:52:82:01:02:03",
		"intel":       "00:1b:21:aa:bb:cc",
		"test-vm":     "bc:24:11:52:02:37",
	} {
		page := libraryGet(t, s, "/machines?q="+urlQuery(query))
		if got := strings.Join(rowOrder(page, all...), " "); got != want {
			t.Fatalf("q=%q: got %q", query, got)
		}
		if !strings.Contains(page, "Найдено: 1 из 3") {
			t.Fatalf("q=%q: no count", query)
		}
	}
	if page := libraryGet(t, s, "/machines?q=nothing-like-this"); !strings.Contains(page, "Ничего не найдено") {
		t.Fatal("no empty search state")
	}
	for sort, want := range map[string]string{
		"first":    "00:1b:21:aa:bb:cc 3c:52:82:01:02:03 bc:24:11:52:02:37",
		"mac":      "00:1b:21:aa:bb:cc 3c:52:82:01:02:03 bc:24:11:52:02:37",
		"model":    "3c:52:82:01:02:03 bc:24:11:52:02:37 00:1b:21:aa:bb:cc",
		"name":     "bc:24:11:52:02:37 00:1b:21:aa:bb:cc 3c:52:82:01:02:03",
		"ip":       "bc:24:11:52:02:37 3c:52:82:01:02:03 00:1b:21:aa:bb:cc",
		"unknown!": "bc:24:11:52:02:37 3c:52:82:01:02:03 00:1b:21:aa:bb:cc",
	} {
		page := libraryGet(t, s, "/machines?sort="+urlQuery(sort))
		if got := strings.Join(rowOrder(page, all...), " "); got != want {
			t.Fatalf("sort=%s: got %s", sort, got)
		}
	}
	// Column links keep the search.
	if page := libraryGet(t, s, "/machines?q=hp"); !strings.Contains(page, `href="/machines?q=hp&amp;sort=name"`) {
		t.Fatal("sort link drops the search")
	}
}

func TestMachinesPageEmptyAndWithoutBook(t *testing.T) {
	empty := &bookStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}, machines: map[string]MachineInfo{}}
	if page := libraryGet(t, &Server{Store: empty}, "/machines"); !strings.Contains(page, "Машин пока нет") {
		t.Fatalf("empty state: %s", page)
	}
	noBook := &Server{Store: &authOnlyStore{user: User{ID: 1, Role: "observer", Enabled: true}}}
	if page := libraryGet(t, noBook, "/machines"); !strings.Contains(page, "Список машин недоступен") {
		t.Fatalf("no book: %s", page)
	}
}

func TestMachinesPageInEnglish(t *testing.T) {
	store := machinesFixture()
	store.user.Language = langEN
	s := &Server{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/machines", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	page := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(page, "<h1>Machines</h1>") {
		t.Fatalf("code=%d %s", rec.Code, page)
	}
	// The note is the machine's data, not interface text.
	page = strings.ReplaceAll(page, "Русский", "")
	note := store.machines["bc:24:11:52:02:37"].Note
	page = strings.ReplaceAll(page, note, "")
	page = strings.ReplaceAll(page, machineListRow{MachineInfo: MachineInfo{Note: note}}.NoteShort(), "")
	if found := cyrillic.FindAllString(page, 5); len(found) > 0 {
		t.Fatalf("Russian text on the English page: %q", found)
	}
}

func urlQuery(v string) string { return url.QueryEscape(v) }
