package ombmigration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"omb/internal/agent"
)

type Object map[string]any
type Manifest struct {
	SchemaVersion  int                         `json:"schema_version"`
	SourceHost     string                      `json:"source_host"`
	CollectedAt    time.Time                   `json:"collected_at"`
	TableCounts    map[string]map[string]int64 `json:"table_counts"`
	Storages       []Object                    `json:"storages"`
	Profiles       []Object                    `json:"windows_profiles"`
	Answers        []Object                    `json:"windows_answers"`
	Drivers        []Object                    `json:"driver_configs"`
	Menus          []Object                    `json:"menu_versions"`
	MenuIntegrity  []Object                    `json:"menu_integrity"`
	Draft          []Object                    `json:"menu_draft_items"`
	ProfileConfigs []agent.WindowsISORequest   `json:"profile_configs"`
	Recovery       Object                      `json:"recovery"`
	Boot           Object                      `json:"boot_snapshot"`
	Files          []Object                    `json:"files"`
	Digest         string                      `json:"-"`
}

func decodeJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e := d.Decode(v); e != nil {
		return e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}
func decodeProfileConfig(b []byte, request *agent.WindowsISORequest) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(request); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing profile configuration data")
	}
	return nil
}
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	var keys map[string]json.RawMessage
	if e := decodeJSON(b, &keys); e != nil {
		return m, fmt.Errorf("invalid manifest JSON")
	}
	for _, k := range []string{"schema_version", "source_host", "collected_at", "table_counts", "storages", "windows_profiles", "windows_answers", "driver_configs", "menu_versions", "menu_integrity", "menu_draft_items", "profile_configs", "recovery", "boot_snapshot"} {
		v, ok := keys[k]
		if !ok || bytes.Equal(v, []byte("null")) {
			return m, fmt.Errorf("manifest missing %s", k)
		}
	}
	if e := decodeJSON(b, &m); e != nil {
		return m, fmt.Errorf("invalid manifest fields")
	}
	if m.SchemaVersion != 1 || m.SourceHost == "" || m.CollectedAt.IsZero() {
		return m, fmt.Errorf("unsupported or incomplete manifest")
	}
	for _, db := range []string{"pxe_boot", "pxe_control", "pxe_images"} {
		if _, ok := m.TableCounts[db]; !ok {
			return m, fmt.Errorf("missing table counts for %s", db)
		}
	}
	if len(m.TableCounts) != 3 {
		return m, fmt.Errorf("unexpected database in manifest")
	}
	for db, tables := range m.TableCounts {
		for table, count := range tables {
			if !identifier(table) || count < 0 {
				return m, fmt.Errorf("invalid table count %s.%s", db, table)
			}
		}
	}
	for kind, objects := range map[string][]Object{"storage": m.Storages, "profile": m.Profiles, "answer": m.Answers, "driver": m.Drivers, "menu": m.Menus} {
		seen := map[int64]bool{}
		for _, o := range objects {
			id := number(o, "id")
			if id <= 0 || seen[id] {
				return m, fmt.Errorf("invalid or duplicate %s ID", kind)
			}
			seen[id] = true
		}
	}
	if text(m.Boot, "path") != "/var/lib/pxe-platform-boot/menu.ipxe" || !validHash(text(m.Boot, "sha256")) {
		return m, fmt.Errorf("invalid Boot snapshot baseline")
	}
	for _, g := range []struct {
		db, table string
		objects   []Object
	}{{"pxe_images", "storages", m.Storages}, {"pxe_control", "windows_profiles", m.Profiles}, {"pxe_control", "windows_answer_variants", m.Answers}, {"pxe_control", "winpe_driver_configs", m.Drivers}, {"pxe_control", "menu_versions", m.Menus}, {"pxe_control", "menu_draft_items", m.Draft}} {
		count, ok := m.TableCounts[g.db][g.table]
		if !ok || count != int64(len(g.objects)) {
			return m, fmt.Errorf("manifest object inventory differs: %s", g.table)
		}
	}
	if len(m.ProfileConfigs) != len(m.Profiles) || len(m.MenuIntegrity) != len(m.Menus) {
		return m, fmt.Errorf("manifest lacks profile/menu integrity observations")
	}
	m.Digest = hashBytes(b)
	return m, nil
}
func text(o Object, k string) string { s, _ := o[k].(string); return s }
func number(o Object, k string) int64 {
	switch n := o[k].(type) {
	case json.Number:
		v, _ := n.Int64()
		return v
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
func hashBytes(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return len(s) == 64 && e == nil && hex.EncodeToString(b) == s
}
func identifier(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func targetDB(s string) string { return strings.Replace(s, "pxe_", "omb_", 1) }
