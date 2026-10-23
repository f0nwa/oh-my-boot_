package control

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseMACAcceptsCommonForms(t *testing.T) {
	for in, want := range map[string]string{
		"BC:24:11:AA:BB:01":    "bc:24:11:aa:bb:01",
		"bc-24-11-aa-bb-01":    "bc:24:11:aa:bb:01",
		"bc2411aabb01":         "bc:24:11:aa:bb:01",
		"bc24.11aa.bb01":       "bc:24:11:aa:bb:01",
		" BC 24 11 AA BB 01 ":  "bc:24:11:aa:bb:01",
		"bc:24:11:aa:bb":       "",
		"bc:24:11:aa:bb:01:02": "",
		"bc:24:11:aa:bb:0g":    "",
		"bc:24:11:aa:bb:01x":   "",
		"00:00:00:00:00:00":    "", // not a card
		"ff:ff:ff:ff:ff:ff":    "", // broadcast
		"01:00:5e:00:00:01":    "", // multicast
		"":                     "",
	} {
		got, ok := parseMAC(in)
		if got != want || ok != (want != "") {
			t.Errorf("parseMAC(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func operatorBook() *bookStore {
	store := machinesFixture()
	store.user.Role = "operator"
	return store
}

func TestAddMachineByHand(t *testing.T) {
	store := operatorBook()
	s := &Server{Store: store}
	res := menuPost(t, s, "/machines", url.Values{"mac": {"BC-24-11-AA-BB-01"}, "name": {"Бухгалтерия-3"}, "product": {"ThinkPad T14"}, "note": {"ждёт установки"}, "sort": {"name"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("add: %d %s", res.Code, res.Body)
	}
	loc := res.Header().Get("Location")
	if !strings.HasPrefix(loc, "/machines?") || !strings.Contains(loc, "sort=name") || !strings.HasSuffix(loc, "#m-bc:24:11:aa:bb:01") {
		t.Fatalf("location=%q", loc)
	}
	m, ok := store.machines["bc:24:11:aa:bb:01"]
	if !ok || !m.Manual || m.Name != "Бухгалтерия-3" || m.Model() != "ThinkPad T14" || m.Note != "ждёт установки" || m.FirstSeen.IsZero() {
		t.Fatalf("stored %+v", m)
	}
	if len(store.audits) != 1 || store.audits[0] != "machine.add ok bc:24:11:aa:bb:01" {
		t.Fatalf("audit=%v", store.audits)
	}
	page := libraryGet(t, s, "/machines")
	if !strings.Contains(page, "ещё не загружалась") || !strings.Contains(page, `id="m-bc:24:11:aa:bb:01"`) {
		t.Fatalf("manual machine not marked: %s", page)
	}
	// The first boot clears the mark.
	_ = store.SeeMachines(context.Background(), []MachineSeen{{MAC: "bc:24:11:aa:bb:01", IP: "10.30.0.50", Firmware: "UEFI", At: time.Now()}})
	if store.machines["bc:24:11:aa:bb:01"].Manual {
		t.Fatal("still manual after booting")
	}
}

func TestAddMachineRejectsBadAndDuplicateMACs(t *testing.T) {
	store := operatorBook()
	s := &Server{Store: store}
	res := menuPost(t, s, "/machines", url.Values{"mac": {"bc:24:11"}, "name": {"keep-me"}})
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "Неверный MAC-адрес") || !strings.Contains(res.Body.String(), `value="keep-me"`) {
		t.Fatalf("bad MAC: %d %s", res.Code, res.Body)
	}
	res = menuPost(t, s, "/machines", url.Values{"mac": {"3C-52-82-01-02-03"}})
	body := res.Body.String()
	if res.Code != http.StatusConflict || !strings.Contains(body, "Такой MAC уже есть в списке") || !strings.Contains(body, `href="/events/machines/3c:52:82:01:02:03">Открыть машину`) {
		t.Fatalf("duplicate: %d %s", res.Code, body)
	}
	if store.machines["3c:52:82:01:02:03"].Name != "" || len(store.audits) != 0 {
		t.Fatal("duplicate changed the machine")
	}
	if res := menuPost(t, s, "/machines", url.Values{"mac": {"bc:24:11:aa:bb:02"}, "name": {strings.Repeat("x", 81)}}); res.Code != http.StatusBadRequest {
		t.Fatalf("long name: %d", res.Code)
	}
}

func TestEditMachineFromTheTable(t *testing.T) {
	store := operatorBook()
	s := &Server{Store: store}
	mac := "3c:52:82:01:02:03"
	res := menuPost(t, s, "/machines/"+mac, url.Values{"name": {"hr-laptop"}, "manufacturer": {"HP"}, "product": {"EliteBook 840 G8"}, "note": {"отдел кадров"}, "q": {"hp"}, "sort": {"ip"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("edit: %d %s", res.Code, res.Body)
	}
	loc, _ := url.Parse(res.Header().Get("Location"))
	if loc.Path != "/machines" || loc.Query().Get("q") != "hp" || loc.Query().Get("sort") != "ip" || loc.Query().Get("message") == "" || loc.Fragment != "m-"+mac {
		t.Fatalf("location=%q", res.Header().Get("Location"))
	}
	if m := store.machines[mac]; m.Name != "hr-laptop" || m.Note != "отдел кадров" || m.Serial != "5CG1234XYZ" {
		t.Fatalf("stored %+v", m)
	}
	if len(store.audits) != 1 || store.audits[0] != "machine.edit ok "+mac {
		t.Fatalf("audit=%v", store.audits)
	}
	// A return address is only ever this page.
	res = menuPost(t, s, "/machines/"+mac, url.Values{"name": {"x"}, "q": {"//evil.example"}, "sort": {"../../x"}})
	if loc := res.Header().Get("Location"); !strings.HasPrefix(loc, "/machines?") || strings.Contains(loc, "sort=") {
		t.Fatalf("location=%q", loc)
	}
	if res := menuPost(t, s, "/machines/not-a-mac", url.Values{"name": {"x"}}); res.Code != http.StatusBadRequest {
		t.Fatalf("bad mac: %d", res.Code)
	}
}

func TestMachineTableRights(t *testing.T) {
	store := machinesFixture() // observer
	s := &Server{Store: store}
	page := libraryGet(t, s, "/machines")
	if strings.Contains(page, `method="post" action="/machines"`) || strings.Contains(page, `action="/machines/bc:24:11:52:02:37"`) || strings.Contains(page, "/delete") || strings.Contains(page, "machines.js") {
		t.Fatal("observer sees edit forms")
	}
	if res := menuPost(t, s, "/machines", url.Values{"mac": {"bc:24:11:aa:bb:01"}}); res.Code != http.StatusForbidden {
		t.Fatalf("observer add: %d", res.Code)
	}
	store.user.Role = "operator"
	page = libraryGet(t, s, "/machines")
	if !strings.Contains(page, `method="post" action="/machines"`) || !strings.Contains(page, `action="/machines/bc:24:11:52:02:37"`) || strings.Contains(page, "/delete") {
		t.Fatal("operator forms wrong")
	}
	if res := menuPost(t, s, "/machines/bc:24:11:52:02:37/delete", url.Values{}); res.Code != http.StatusForbidden {
		t.Fatalf("operator delete: %d", res.Code)
	}
	store.user.Role = "admin"
	page = libraryGet(t, s, "/machines")
	if !strings.Contains(page, `action="/machines/bc:24:11:52:02:37/delete"`) {
		t.Fatal("admin has no delete")
	}
	res := menuPost(t, s, "/machines/bc:24:11:52:02:37/delete", url.Values{"q": {"vm"}})
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/machines?") || !strings.Contains(res.Header().Get("Location"), "q=vm") {
		t.Fatalf("admin delete: %d %q", res.Code, res.Header().Get("Location"))
	}
	if _, ok := store.machines["bc:24:11:52:02:37"]; ok || store.audits[len(store.audits)-1] != "machine.delete ok bc:24:11:52:02:37" {
		t.Fatalf("not deleted: %v", store.audits)
	}
}
