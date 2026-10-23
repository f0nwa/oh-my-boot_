package control

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// rulesBook is a book of machines with prefix rules.
type rulesBook struct {
	*bookStore
	rules map[string]MacRule
}

func (b *rulesBook) MacRules(context.Context) ([]MacRule, error) {
	var out []MacRule
	for _, r := range b.rules {
		out = append(out, r)
	}
	return out, nil
}

func (b *rulesBook) AddMacRule(_ context.Context, r MacRule) (bool, error) {
	if _, ok := b.rules[r.Prefix]; ok {
		return false, nil
	}
	b.rules[r.Prefix] = r
	return true, nil
}

func (b *rulesBook) EditMacRule(_ context.Context, r MacRule) error {
	b.rules[r.Prefix] = r
	return nil
}

func (b *rulesBook) DeleteMacRule(_ context.Context, prefix string) error {
	delete(b.rules, prefix)
	return nil
}

func newRulesBook(role string) *rulesBook {
	store := machinesFixture()
	store.user.Role = role
	store.machines["bc:24:11:04:2a:ed"] = MachineInfo{MAC: "bc:24:11:04:2a:ed", FirstSeen: time.Now().Add(-time.Hour), LastSeen: time.Now()}
	return &rulesBook{bookStore: store, rules: map[string]MacRule{
		"bc:24:11":    {Prefix: "bc:24:11", Title: "Стенд Proxmox", Manufacturer: "Proxmox (виртуальная карта)"},
		"bc:24:11:52": {Prefix: "bc:24:11:52", Title: "Тестовые ВМ 52"},
	}}
}

func TestParseMACPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"BC:24:11":          "bc:24:11",
		"bc-24-11-52":       "bc:24:11:52",
		"bc2411520":         "", // odd digits
		"bc24115202":        "bc:24:11:52:02",
		"bc:24":             "",
		"bc:24:11:52:02:37": "", // a whole MAC is not a prefix
		"01:00:5e":          "", // multicast
		"zz:24:11":          "",
	} {
		got, ok := parseMACPrefix(in)
		if got != want || ok != (want != "") {
			t.Errorf("parseMACPrefix(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestMacRulesPickTheLongestPrefixAndLabelMachines(t *testing.T) {
	rules := macRules{{Prefix: "bc:24:11", Title: "Стенд Proxmox", Manufacturer: "Proxmox (виртуальная карта)"}, {Prefix: "bc:24:11:52", Title: "Тестовые ВМ 52"}}
	if r := rules.match("bc:24:11:52:02:37"); r == nil || r.Prefix != "bc:24:11:52" {
		t.Fatalf("longest: %+v", r)
	}
	if r := rules.match("bc:24:11:04:2a:ed"); r == nil || r.Prefix != "bc:24:11" {
		t.Fatalf("short: %+v", r)
	}
	if r := rules.match("00:1b:21:aa:bb:cc"); r != nil {
		t.Fatalf("no rule expected: %+v", r)
	}
	// Order: model (person's or BIOS) → rule → IEEE.
	withModel := MachineInfo{MAC: "bc:24:11:52:02:37", Manufacturer: "QEMU", Product: "Standard PC"}
	withModel.Rule = rules.match(withModel.MAC)
	if got := machineLabel(&withModel, withModel.MAC); got != "QEMU Standard PC" {
		t.Fatalf("model label=%q", got)
	}
	bare := MachineInfo{MAC: "bc:24:11:04:2a:ed", Rule: rules.match("bc:24:11:04:2a:ed")}
	if got := machineLabel(&bare, bare.MAC); got != "Стенд Proxmox" {
		t.Fatalf("rule label=%q", got)
	}
	// The rule's maker names the network card instead of the IEEE entry.
	if got := bare.NICVendor(); got != "Proxmox (виртуальная карта)" {
		t.Fatalf("nic=%q", got)
	}
	ieee := MachineInfo{MAC: "00:1b:21:aa:bb:cc"}
	if got := machineLabel(&ieee, ieee.MAC); got != "Intel Corporate" {
		t.Fatalf("ieee label=%q", got)
	}
	// Rows of the Now page and the journal get the rule too.
	rows := []machineRow{{MAC: "bc:24:11:04:2a:ed"}, {MAC: "aa:00:00:00:00:01"}}
	describeMachines(rows, map[string]MachineInfo{}, rules)
	if rows[0].Label != "Стенд Proxmox" {
		t.Fatalf("row label=%q", rows[0].Label)
	}
}

func TestMacRulesOnTheMachinesPage(t *testing.T) {
	store := newRulesBook("operator")
	s := &Server{Store: store}
	page := libraryGet(t, s, "/machines")
	for _, want := range []string{"Правила по префиксу MAC", "bc:24:11:52", "Тестовые ВМ 52", `href="/machines?rule=bc%3A24%3A11"`, `action="/machine-rules"`, "Стенд Proxmox"} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "/machine-rules/bc:24:11/delete") {
		t.Fatal("operator sees rule delete")
	}
	// Filter by rule: every machine whose MAC starts with the prefix.
	page = libraryGet(t, s, "/machines?rule=bc:24:11")
	if !strings.Contains(page, "«Стенд Proxmox»") || !strings.Contains(page, "Найдено: 2 из 4") || strings.Contains(page, `id="m-00:1b:21:aa:bb:cc"`) {
		t.Fatalf("rule filter: %s", page)
	}
	// Add, reject a duplicate and a bad prefix, edit.
	res := menuPost(t, s, "/machine-rules", url.Values{"prefix": {"00-1B-21"}, "title": {"Ноутбуки Intel"}, "manufacturer": {"Intel"}})
	if res.Code != http.StatusSeeOther || store.rules["00:1b:21"].Title != "Ноутбуки Intel" {
		t.Fatalf("add: %d %+v", res.Code, store.rules)
	}
	if res := menuPost(t, s, "/machine-rules", url.Values{"prefix": {"00:1b:21"}, "title": {"x"}}); res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "Правило для этого префикса уже есть") {
		t.Fatalf("duplicate: %d", res.Code)
	}
	if res := menuPost(t, s, "/machine-rules", url.Values{"prefix": {"00:1b"}, "title": {"x"}}); res.Code != http.StatusBadRequest {
		t.Fatalf("bad prefix: %d", res.Code)
	}
	if res := menuPost(t, s, "/machine-rules", url.Values{"prefix": {"00:1c:21"}, "title": {""}}); res.Code != http.StatusBadRequest {
		t.Fatalf("no title: %d", res.Code)
	}
	res = menuPost(t, s, "/machine-rules/00:1b:21", url.Values{"title": {"Ноутбуки"}, "manufacturer": {""}, "note": {"офис"}})
	if res.Code != http.StatusSeeOther || store.rules["00:1b:21"].Title != "Ноутбуки" || store.rules["00:1b:21"].Note != "офис" {
		t.Fatalf("edit: %d %+v", res.Code, store.rules["00:1b:21"])
	}
	if page := libraryGet(t, s, "/machines"); !strings.Contains(page, "Ноутбуки") {
		t.Fatal("rule label not on the machine without a model")
	}
	if res := menuPost(t, s, "/machine-rules/00:1b:21/delete", url.Values{}); res.Code != http.StatusForbidden {
		t.Fatalf("operator delete: %d", res.Code)
	}
	store.user.Role = "admin"
	if res := menuPost(t, s, "/machine-rules/00:1b:21/delete", url.Values{}); res.Code != http.StatusSeeOther || len(store.rules) != 2 {
		t.Fatalf("admin delete: %d", res.Code)
	}
	want := []string{"mac_rule.add ok 00:1b:21", "mac_rule.edit ok 00:1b:21", "mac_rule.delete ok 00:1b:21"}
	var got []string
	for _, a := range store.audits {
		if strings.HasPrefix(a, "mac_rule.") {
			got = append(got, a)
		}
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("audit=%v", store.audits)
	}
	// Observers see the rules but no forms.
	store.user.Role = "observer"
	page = libraryGet(t, s, "/machines")
	if !strings.Contains(page, "Тестовые ВМ 52") || strings.Contains(page, `action="/machine-rules`) {
		t.Fatal("observer view of rules")
	}
}

func TestMachinePageShowsTheRule(t *testing.T) {
	store := newRulesBook("observer")
	page := libraryGet(t, &Server{Store: store}, "/events/machines/bc:24:11:04:2a:ed")
	if !strings.Contains(page, "Стенд Proxmox") || !strings.Contains(page, `href="/machines?rule=bc%3A24%3A11"`) || !strings.Contains(page, "Proxmox (виртуальная карта)") {
		t.Fatalf("machine page: %s", page)
	}
}

func TestMacRulesPageInEnglish(t *testing.T) {
	store := newRulesBook("admin")
	store.user.Language = langEN
	page := libraryGet(t, &Server{Store: store}, "/machines?rule=bc:24:11")
	for _, data := range []string{"Русский", "Стенд Proxmox", "Тестовые ВМ 52", "Proxmox (виртуальная карта)", "стенд для проверки установки Windows 11 с драйверами и ответами", "стенд для проверки установки Windows 11…"} {
		page = strings.ReplaceAll(page, data, "")
	}
	if !strings.Contains(page, "MAC prefix rules") {
		t.Fatal("no rules section")
	}
	if found := cyrillic.FindAllString(page, 5); len(found) > 0 {
		t.Fatalf("Russian text on the English page: %q", found)
	}
}
