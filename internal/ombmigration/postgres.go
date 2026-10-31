package ombmigration

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type Runner interface {
	Run(context.Context, string, []string, []byte) ([]byte, error)
}
type RunFunc func(context.Context, string, []string, []byte) ([]byte, error)

func (f RunFunc) Run(c context.Context, n string, a []string, in []byte) ([]byte, error) {
	return f(c, n, a, in)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(string(input))
	out, e := cmd.Output()
	if e != nil {
		return nil, fmt.Errorf("%s execution failed", name)
	}
	return out, nil
}

type PSQL struct{ Runner Runner }

func (p PSQL) Query(ctx context.Context, db, sql string) ([]byte, error) {
	switch db {
	case "pxe_boot", "pxe_control", "pxe_images", "omb_boot", "omb_control", "omb_images":
	default:
		return nil, fmt.Errorf("unsupported database")
	}
	r := p.Runner
	if r == nil {
		r = ExecRunner{}
	}
	return r.Run(ctx, "runuser", []string{"-u", "postgres", "--", "psql", "-XAt", "-v", "ON_ERROR_STOP=1", "-d", db}, []byte(sql))
}

type Snapshot map[string]map[string][]Object

// Snapshot reads every public table, including unknown authentication tables.
// The snapshot is private; callers must never print it.
func (p PSQL) Snapshot(ctx context.Context, renamed bool) (Snapshot, error) {
	result := Snapshot{}
	for _, source := range []string{"pxe_boot", "pxe_control", "pxe_images"} {
		db := source
		if renamed {
			db = targetDB(source)
		}
		raw, e := p.Query(ctx, db, `SELECT COALESCE(json_agg(tablename ORDER BY tablename),'[]'::json) FROM pg_tables WHERE schemaname='public';`)
		if e != nil {
			return nil, fmt.Errorf("cannot inspect %s", db)
		}
		var names []string
		if decodeJSON(raw, &names) != nil {
			return nil, fmt.Errorf("invalid table inventory for %s", db)
		}
		tables := map[string][]Object{}
		for _, table := range names {
			if !identifier(table) {
				return nil, fmt.Errorf("unsupported table identifier in %s", db)
			}
			raw, e = p.Query(ctx, db, `SELECT COALESCE(json_agg(r ORDER BY r::text),'[]'::json) FROM (SELECT row_to_json(t) r FROM public."`+table+`" t) q;`)
			if e != nil {
				return nil, fmt.Errorf("cannot inspect %s.%s", db, table)
			}
			var rows []Object
			if decodeJSON(raw, &rows) != nil {
				return nil, fmt.Errorf("invalid rows for %s.%s", db, table)
			}
			tables[table] = rows
		}
		result[source] = tables
	}
	return result, nil
}
func (s Snapshot) rows(db, table string) []Object { return s[db][table] }
func (s Snapshot) find(db, table string, id int64) Object {
	for _, o := range s.rows(db, table) {
		if number(o, "id") == id {
			return o
		}
	}
	return nil
}
func cloneSnapshot(s Snapshot) Snapshot {
	b, _ := jsonBytes(s)
	var c Snapshot
	_ = decodeJSON(b, &c)
	return c
}
func sortedKeys[V any](m map[string]V) []string {
	k := make([]string, 0, len(m))
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}
