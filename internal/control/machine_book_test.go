package control

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"omb/internal/boot"
)

type bookStore struct {
	authOnlyStore
	machines map[string]MachineInfo
	audits   []string
}

func (b *bookStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	b.audits = append(b.audits, action+" "+result+" "+detail)
	return nil
}

func (b *bookStore) Machines(context.Context) ([]MachineInfo, error) {
	var out []MachineInfo
	for _, m := range b.machines {
		out = append(out, m)
	}
	return out, nil
}

func (b *bookStore) SeeMachines(_ context.Context, seen []MachineSeen) error {
	for _, s := range seen {
		m, ok := b.machines[s.MAC]
		if !ok {
			m = MachineInfo{MAC: s.MAC, FirstSeen: s.At}
		}
		m.LastSeen, m.LastIP, m.Firmware, m.Manual = s.At, s.IP, s.Firmware, false
		if !s.DetectedAt.IsZero() && (m.DetectedAt == nil || s.DetectedAt.After(*m.DetectedAt)) {
			at := s.DetectedAt
			m.Manufacturer, m.Product, m.Serial, m.UUID, m.DetectedAt = s.Manufacturer, s.Product, s.Serial, s.UUID, &at
		}
		b.machines[s.MAC] = m
	}
	return nil
}

func (b *bookStore) EditMachine(_ context.Context, mac string, e MachineEdit) error {
	m := b.machines[mac]
	m.MAC, m.Name, m.Note, m.ManufacturerOverride, m.ProductOverride = mac, e.Name, e.Note, e.Manufacturer, e.Product
	b.machines[mac] = m
	return nil
}

func (b *bookStore) AddMachine(_ context.Context, mac string, e MachineEdit) (bool, error) {
	if _, ok := b.machines[mac]; ok {
		return false, nil
	}
	now := time.Now()
	b.machines[mac] = MachineInfo{MAC: mac, FirstSeen: now, LastSeen: now, Manual: true, Name: e.Name, Note: e.Note, ManufacturerOverride: e.Manufacturer, ProductOverride: e.Product}
	return true, nil
}

func (b *bookStore) ApplyMachineImport(ctx context.Context, items []machineImportChange) error {
	for _, it := range items {
		if it.Add {
			_, _ = b.AddMachine(ctx, it.MAC, it.Edit)
			continue
		}
		_ = b.EditMachine(ctx, it.MAC, it.Edit)
	}
	return nil
}

func (b *bookStore) DeleteMachine(_ context.Context, mac string) error {
	delete(b.machines, mac)
	return nil
}

func helloEvent(mac string, at time.Time, query string) boot.Event {
	return boot.Event{Kind: "hello", MAC: mac, At: at, RemoteAddr: "10.30.0.17:1", Path: "/boot/hello?" + query}
}

func TestHelloReportsFillTheBookButAreNotBootSteps(t *testing.T) {
	t0 := time.Date(2026, 10, 23, 15, 0, 0, 0, time.UTC)
	mac := "bc:24:11:52:02:37"
	events := []boot.Event{
		{At: t0, Kind: "menu", MAC: mac, RemoteAddr: "10.30.0.17:1", Path: "/boot/menu.ipxe"},
		helloEvent(mac, t0.Add(time.Second), "manufacturer=To+Be+Filled+By+O.E.M.&product=QEMU+Standard+PC&serial=&uuid=00000000-0000-0000-0000-000000000000"),
		helloEvent(mac, t0.Add(time.Minute), "manufacturer=Dell+Inc.&product=OptiPlex+7090&serial=7XK2JQ3"),
	}
	rows := machineRows(events, nil, nil)
	if len(rows) != 1 || rows[0].Stage != "menu" {
		t.Fatalf("a hello report counted as a boot step: %+v", rows)
	}
	seen := machinesSeen(rows, events)
	if len(seen) != 1 || seen[0].Manufacturer != "Dell Inc." || seen[0].Product != "OptiPlex 7090" || seen[0].Serial != "7XK2JQ3" || !seen[0].DetectedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("seen=%+v", seen)
	}
	if _, product, _, uuid, _ := helloDetails(events[1]); product != "QEMU Standard PC" || uuid != "" {
		t.Fatalf("placeholders kept: %q %q", product, uuid)
	}
	info := MachineInfo{MAC: mac, Manufacturer: "Dell Inc.", Product: "OptiPlex 7090"}
	if got := machineLabel(&info, mac); got != "Dell Inc. OptiPlex 7090" {
		t.Fatalf("label=%q", got)
	}
	info.ProductOverride = "OptiPlex 7090 (бухгалтерия)"
	if got := machineLabel(&info, mac); got != "Dell Inc. OptiPlex 7090 (бухгалтерия)" {
		t.Fatalf("override label=%q", got)
	}
	if got := machineLabel(nil, mac); got != "Proxmox Server Solutions GmbH" {
		t.Fatalf("nic label=%q", got)
	}
	// The cache writes a machine again only when something moved.
	var cache machineBookCache
	if changed := cache.changed(seen); len(changed) != 1 {
		t.Fatal("new machine not written")
	}
	cache.remember(seen)
	if changed := cache.changed(seen); len(changed) != 0 {
		t.Fatal("unchanged machine written again")
	}
}

func TestMachinePageNamesAndEditsAMachine(t *testing.T) {
	mac := "bc:24:11:52:02:37"
	store := &bookStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, machines: map[string]MachineInfo{
		mac: {MAC: mac, FirstSeen: time.Now().Add(-time.Hour), LastSeen: time.Now(), Manufacturer: "QEMU", Product: "Standard PC (Q35 + ICH9, 2009)", Serial: "SN1"},
	}}
	s := &Server{Store: store}
	page := libraryGet(t, s, "/events/machines/"+mac)
	for _, want := range []string{"Standard PC (Q35 &#43; ICH9, 2009)", "SN1", "Proxmox Server Solutions GmbH", `action="/events/machines/` + mac + `/edit"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %q: %s", want, page)
		}
	}
	if strings.Contains(page, "/delete") {
		t.Fatal("operator sees delete")
	}
	res := menuPost(t, s, "/events/machines/"+mac+"/edit", url.Values{"name": {"test-vm-17"}, "product": {"Lab VM"}, "note": {"стенд"}})
	if res.Code != http.StatusSeeOther || store.machines[mac].Name != "test-vm-17" || store.machines[mac].Model() != "Lab VM" {
		t.Fatalf("edit: %d %+v", res.Code, store.machines[mac])
	}
	if page := libraryGet(t, s, "/events/machines/"+mac); !strings.Contains(page, "<h1>test-vm-17</h1>") {
		t.Fatalf("named page: %s", page)
	}
	if res := menuPost(t, s, "/events/machines/"+mac+"/edit", url.Values{"name": {strings.Repeat("x", 81)}}); res.Code != http.StatusBadRequest {
		t.Fatalf("long name: %d", res.Code)
	}
	if res := menuPost(t, s, "/events/machines/"+mac+"/delete", url.Values{}); res.Code != http.StatusForbidden {
		t.Fatalf("operator delete: %d", res.Code)
	}
	store.user.Role = "admin"
	if res := menuPost(t, s, "/events/machines/"+mac+"/delete", url.Values{}); res.Code != http.StatusSeeOther || len(store.machines) != 0 {
		t.Fatalf("admin delete: %d", res.Code)
	}
}

func (b *bookStore) SetMachineNetworkDriver(_ context.Context, mac, driver string) error {
	m := b.machines[mac]
	m.NetworkDriver = driver
	b.machines[mac] = m
	return nil
}
