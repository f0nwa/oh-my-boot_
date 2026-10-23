package control

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"pxe-platform/internal/boot"
	"pxe-platform/internal/oui"
)

// MachineInfo is one machine in the book of machines: what the logs and the
// machine itself reported, and what people wrote about it.
type MachineInfo struct {
	MAC                   string
	FirstSeen, LastSeen   time.Time
	LastIP, Firmware      string
	Manufacturer, Product string // from SMBIOS, reported by the menu
	Serial, UUID          string
	DetectedAt            *time.Time
	Name, Note            string
	ManufacturerOverride  string
	ProductOverride       string
	// Manual marks a machine a person added by MAC before it ever booted.
	Manual bool
}

// MachineSeen is what one look at the logs says about a machine.
type MachineSeen struct {
	MAC, IP, Firmware     string
	At                    time.Time
	Manufacturer, Product string
	Serial, UUID          string
	DetectedAt            time.Time // zero without a hello report
}

// MachineEdit is what a person may change about a machine.
type MachineEdit struct {
	Name, Note, Manufacturer, Product string
}

// MachineBook stores the book of machines. It is optional: a store without it
// shows machines only from the logs.
type MachineBook interface {
	Machines(context.Context) ([]MachineInfo, error)
	SeeMachines(context.Context, []MachineSeen) error
	// AddMachine adds a machine by hand; false when the MAC is already there.
	AddMachine(context.Context, string, MachineEdit) (bool, error)
	EditMachine(context.Context, string, MachineEdit) error
	DeleteMachine(context.Context, string) error
}

// Maker is the machine's maker: what a person wrote, else SMBIOS.
func (m MachineInfo) Maker() string {
	if m.ManufacturerOverride != "" {
		return m.ManufacturerOverride
	}
	return m.Manufacturer
}

// Model is the machine's model: what a person wrote, else SMBIOS.
func (m MachineInfo) Model() string {
	if m.ProductOverride != "" {
		return m.ProductOverride
	}
	return m.Product
}

// NICVendor names the maker of the network card from the MAC prefix.
func (m MachineInfo) NICVendor() string { return nicVendor(m.MAC) }

func nicVendor(mac string) string {
	switch vendor := oui.Lookup(mac); vendor {
	case oui.Local:
		return "локальный адрес"
	default:
		return vendor
	}
}

// machineLabel is the short description shown under a MAC: maker and model
// when known, else the network card's maker.
func machineLabel(info *MachineInfo, mac string) string {
	if info != nil {
		maker, model := info.Maker(), info.Model()
		switch {
		case maker != "" && model != "" && !strings.HasPrefix(strings.ToLower(model), strings.ToLower(maker)):
			return maker + " " + model
		case model != "":
			return model
		case maker != "":
			return maker
		}
	}
	if vendor := nicVendor(mac); vendor != "" {
		return vendor
	}
	return ""
}

// helloDetails reads a /boot/hello request logged by Boot.
func helloDetails(e boot.Event) (manufacturer, product, serial, uuid string, ok bool) {
	if e.Kind != "hello" {
		return "", "", "", "", false
	}
	_, raw, _ := strings.Cut(e.Path, "?")
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "", "", "", "", false
	}
	clean := func(v string) string {
		v = strings.TrimSpace(v)
		// Placeholders firmware fills in when nobody set the field.
		switch strings.ToLower(v) {
		case "", "to be filled by o.e.m.", "default string", "system serial number", "system product name", "system manufacturer", "not specified", "none", "0", "00000000-0000-0000-0000-000000000000":
			return ""
		}
		return v
	}
	return clean(q.Get("manufacturer")), clean(q.Get("product")), clean(q.Get("serial")), clean(q.Get("uuid")), true
}

// machinesSeen sums up the logs per MAC for the book: the latest IP and
// firmware from the machine rows and the latest hello report.
func machinesSeen(rows []machineRow, httpEvents []boot.Event) []MachineSeen {
	byMAC := map[string]*MachineSeen{}
	for _, row := range rows {
		if !macAddress.MatchString(row.MAC) {
			continue
		}
		byMAC[row.MAC] = &MachineSeen{MAC: row.MAC, IP: row.IP, Firmware: row.Firmware, At: row.LastSeen}
	}
	for _, e := range httpEvents {
		seen, ok := byMAC[e.MAC]
		if !ok {
			continue
		}
		manufacturer, product, serial, uuid, ok := helloDetails(e)
		if !ok || !e.At.After(seen.DetectedAt) {
			continue
		}
		seen.Manufacturer, seen.Product, seen.Serial, seen.UUID, seen.DetectedAt = manufacturer, product, serial, uuid, e.At
	}
	out := make([]MachineSeen, 0, len(byMAC))
	for _, seen := range byMAC {
		out = append(out, *seen)
	}
	return out
}

// machineBookCache keeps the book in memory between page loads and writes
// only what changed: a new machine, a new hello report, or a last-seen time
// that moved by more than a minute.
type machineBookCache struct {
	mu      sync.Mutex
	written map[string]MachineSeen
}

func (c *machineBookCache) changed(seen []MachineSeen) []MachineSeen {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.written == nil {
		c.written = map[string]MachineSeen{}
	}
	var out []MachineSeen
	for _, s := range seen {
		old, ok := c.written[s.MAC]
		if ok && s.At.Sub(old.At) < time.Minute && s.IP == old.IP && s.Firmware == old.Firmware && !s.DetectedAt.After(old.DetectedAt) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (c *machineBookCache) remember(seen []MachineSeen) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range seen {
		c.written[s.MAC] = s
	}
}

func (c *machineBookCache) forget(mac string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.written, mac)
}

// machineBook records what the logs show and returns the book by MAC; nil
// when the store keeps no book.
func (s *Server) machineBook(ctx context.Context, rows []machineRow, httpEvents []boot.Event) map[string]MachineInfo {
	book, ok := s.Store.(MachineBook)
	if !ok {
		return nil
	}
	if changed := s.bookCache.changed(machinesSeen(rows, httpEvents)); len(changed) > 0 {
		if err := book.SeeMachines(ctx, changed); err == nil {
			s.bookCache.remember(changed)
		}
	}
	machines, err := book.Machines(ctx)
	if err != nil {
		return nil
	}
	out := make(map[string]MachineInfo, len(machines))
	for _, m := range machines {
		out[m.MAC] = m
	}
	return out
}
