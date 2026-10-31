package ombmigration

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func snapshotHash(s Snapshot) string {
	normalized := map[string]map[string][]string{}
	for db, tables := range s {
		normalized[db] = map[string][]string{}
		for table, rows := range tables {
			v := make([]string, 0, len(rows))
			for _, row := range rows {
				b, _ := json.Marshal(row)
				v = append(v, string(b))
			}
			sort.Strings(v)
			normalized[db][table] = v
		}
	}
	b, _ := json.Marshal(normalized)
	return hashBytes(b)
}
func operationalTable(db, table string) bool {
	if db == "pxe_boot" {
		return table == "boot_events"
	}
	if db != "pxe_control" {
		return false
	}
	switch table {
	case "audit_events", "machines", "sessions", "mail_outbox", "mail_tokens", "oidc_flows", "oidc_logout_jti", "recovery_rate_limits":
		return true
	}
	return false
}
func (s Snapshot) validate(m Manifest, preview bool) error {
	for db, counts := range m.TableCounts {
		tables, ok := s[db]
		if !ok || len(tables) != len(counts) {
			return fmt.Errorf("table inventory differs: %s", db)
		}
		for table, count := range counts {
			rows, ok := tables[table]
			if !ok || int64(len(rows)) != count && !(preview && operationalTable(db, table)) {
				return fmt.Errorf("table count differs: %s.%s", db, table)
			}
		}
	}
	for _, group := range []struct {
		db, table string
		objects   []Object
		fields    []string
	}{
		{"pxe_images", "storages", m.Storages, []string{"name", "kind", "root", "storage_key", "server", "share", "subpath", "state"}},
		{"pxe_control", "windows_profiles", m.Profiles, []string{"storage_id", "path", "storage_root", "size_bytes", "modified_at", "state"}},
		{"pxe_control", "windows_answer_variants", m.Answers, []string{"profile_id", "sha256", "size_bytes", "state"}},
		{"pxe_control", "winpe_driver_configs", m.Drivers, []string{"profile_id", "answer_id", "state", "wim_key", "wim_sha256", "script_sha256"}},
	} {
		for _, baseline := range group.objects {
			id := number(baseline, "id")
			row := s.find(group.db, group.table, id)
			if row == nil {
				return fmt.Errorf("missing %s %d", group.table, id)
			}
			for _, field := range group.fields {
				want, present := baseline[field]
				if !present {
					continue
				}
				got := row[field]
				if field == "modified_at" {
					a, e1 := time.Parse(time.RFC3339Nano, fmt.Sprint(want))
					b, e2 := time.Parse(time.RFC3339Nano, fmt.Sprint(got))
					if e1 == nil && e2 == nil && a.Equal(b) {
						continue
					}
				}
				wb, _ := json.Marshal(want)
				gb, _ := json.Marshal(got)
				if string(wb) != string(gb) {
					return fmt.Errorf("baseline differs: %s %d field %s", group.table, id, field)
				}
			}
		}
	}
	for _, baseline := range m.Menus {
		id := number(baseline, "id")
		row := s.find("pxe_control", "menu_versions", id)
		if row == nil {
			return fmt.Errorf("missing menu %d", id)
		}
		script := text(row, "script")
		if hashBytes([]byte(script)) != text(baseline, "sha256") || text(row, "sha256") != text(baseline, "sha256") || number(baseline, "bytes") != int64(len(script)) || row["active"] != baseline["active"] {
			return fmt.Errorf("menu integrity differs: %d", id)
		}
	}
	for _, v := range m.MenuIntegrity {
		if text(v, "stored_sha256") != text(v, "actual_sha256") {
			return fmt.Errorf("baseline menu integrity invalid: %d", number(v, "id"))
		}
	}
	for _, draft := range m.Draft {
		found := false
		for _, row := range s.rows("pxe_control", "menu_draft_items") {
			if number(row, "position") == number(draft, "position") {
				found = true
				for k, want := range draft {
					a, _ := json.Marshal(want)
					b, _ := json.Marshal(row[k])
					if string(a) != string(b) {
						return fmt.Errorf("draft differs at position %d", number(draft, "position"))
					}
				}
			}
		}
		if !found {
			return fmt.Errorf("missing draft position %d", number(draft, "position"))
		}
	}
	return nil
}
func quoteSQL(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func validateSnapshotDelta(before, after Snapshot) error {
	if len(before) != len(after) {
		return fmt.Errorf("migration cannot change database inventory")
	}
	for db, tables := range before {
		next, ok := after[db]
		if !ok || len(tables) != len(next) {
			return fmt.Errorf("migration cannot change table inventory")
		}
		for table, rows := range tables {
			updated, ok := next[table]
			if !ok || len(rows) != len(updated) {
				return fmt.Errorf("migration cannot insert/delete %s", table)
			}
			if snapshotHash(Snapshot{db: {table: rows}}) == snapshotHash(Snapshot{db: {table: updated}}) {
				continue
			}
			byID := map[int64]Object{}
			for _, row := range rows {
				id := number(row, "id")
				if id <= 0 || byID[id] != nil {
					return fmt.Errorf("unsupported changed table identity: %s", table)
				}
				byID[id] = row
			}
			seen := map[int64]bool{}
			for _, row := range updated {
				id := number(row, "id")
				old := byID[id]
				if old == nil || seen[id] || len(old) != len(row) {
					return fmt.Errorf("migration cannot change row identity: %s", table)
				}
				seen[id] = true
				for key, value := range old {
					now, present := row[key]
					if !present {
						return fmt.Errorf("migration cannot remove column: %s", table)
					}
					if string(mustJSON(value)) != string(mustJSON(now)) && !allowedColumn(table, key) {
						return fmt.Errorf("unsupported update %s.%s", table, key)
					}
				}
			}
		}
	}
	return nil
}
func (p PSQL) Commit(ctx context.Context, before, after Snapshot) error {
	// Validate the complete cross-database delta before starting the first
	// transaction, including opaque tables without numeric primary keys.
	if err := validateSnapshotDelta(before, after); err != nil {
		return err
	}
	for _, source := range []string{"pxe_images", "pxe_control", "pxe_boot"} {
		oldTables, newTables := before[source], after[source]
		a, _ := json.Marshal(oldTables)
		b, _ := json.Marshal(newTables)
		if string(a) == string(b) {
			continue
		}
		var sql strings.Builder
		sql.WriteString("BEGIN; SET LOCAL standard_conforming_strings=on;\n")
		for _, table := range sortedKeys(oldTables) {
			sql.WriteString(`LOCK TABLE public."` + table + `" IN SHARE ROW EXCLUSIVE MODE;` + "\n")
		}
		// Whole-table comparison under locks protects opaque auth data as well as
		// known objects. No role, schema, trigger, sequence or ciphertext is changed.
		for _, table := range sortedKeys(oldTables) {
			oldJSON, _ := json.Marshal(oldTables[table])
			tag := "$omb_guard$"
			for strings.Contains(string(oldJSON), tag) {
				tag = strings.TrimSuffix(tag, "$") + "x$"
			}
			sql.WriteString(`DO ` + tag + ` BEGIN IF (SELECT COALESCE(jsonb_agg(x ORDER BY x::text),'[]'::jsonb) FROM (SELECT to_jsonb(t) x FROM public."` + table + `" t) q) IS DISTINCT FROM (SELECT COALESCE(jsonb_agg(x ORDER BY x::text),'[]'::jsonb) FROM jsonb_array_elements(` + quoteSQL(string(oldJSON)) + `::jsonb) x) THEN RAISE EXCEPTION 'migration source changed'; END IF; END ` + tag + `;` + "\n")
			for _, newRow := range newTables[table] {
				id := number(newRow, "id")
				if id <= 0 {
					continue
				}
				var oldRow Object
				for _, v := range oldTables[table] {
					if number(v, "id") == id {
						oldRow = v
						break
					}
				}
				if oldRow == nil {
					return fmt.Errorf("migration cannot insert %s", table)
				}
				fields := []string{}
				for _, key := range sortedKeys(newRow) {
					v, _ := json.Marshal(newRow[key])
					old, _ := json.Marshal(oldRow[key])
					if string(v) == string(old) {
						continue
					}
					if !allowedColumn(table, key) {
						return fmt.Errorf("unsupported update %s.%s", table, key)
					}
					fields = append(fields, `"`+key+`"=(jsonb_populate_record(NULL::public."`+table+`",`+quoteSQL(string(mustJSON(newRow)))+`::jsonb))."`+key+`"`)
				}
				if len(fields) > 0 {
					sql.WriteString(fmt.Sprintf(`UPDATE public."%s" SET %s WHERE id=%d;`+"\n", table, strings.Join(fields, ","), id))
				}
			}
		}
		sql.WriteString("COMMIT;\n")
		if _, err := p.Query(ctx, targetDB(source), sql.String()); err != nil {
			return fmt.Errorf("database transaction failed: %s; inspect journal before retry", targetDB(source))
		}
	}
	return nil
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func allowedColumn(table, key string) bool {
	switch table {
	case "storages":
		return key == "root"
	case "windows_profiles":
		return key == "storage_root"
	case "windows_answer_variants":
		return key == "sha256" || key == "size_bytes"
	case "winpe_driver_configs":
		return key == "wim_key" || key == "wim_sha256" || key == "script_sha256"
	case "menu_versions":
		return key == "script" || key == "sha256"
	}
	return false
}
