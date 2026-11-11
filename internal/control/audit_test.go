package control

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func auditTestStore(t *testing.T) Store {
	t.Helper()
	for _, binary := range []string{"initdb", "pg_ctl"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("PostgreSQL integration requires %s", binary)
		}
	}
	root, err := os.MkdirTemp("/tmp", "omb-control-audit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	data := filepath.Join(root, "db")
	run := func(name string, args ...string) {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
	}
	run("initdb", "-D", data, "-A", "trust", "--no-locale", "-U", "postgres")
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "server.log"), "-o", fmt.Sprintf("-F -h '' -k %s", root), "-w", "start")
	t.Cleanup(func() {
		out, err := exec.Command("pg_ctl", "-D", data, "-m", "immediate", "-w", "stop").CombinedOutput()
		if err != nil {
			t.Errorf("stop PostgreSQL: %v\n%s", err, out)
		}
	})
	pool, err := pgxpool.New(context.Background(), fmt.Sprintf("host=%s user=postgres dbname=postgres", root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	sql, err := os.ReadFile("../../deploy/sql/control.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatal(err)
	}
	return Store{Pool: pool}
}

func TestAuditPaged(t *testing.T) {
	store := auditTestStore(t)
	ctx := context.Background()

	// Insert 120 audit records
	for i := 0; i < 120; i++ {
		err := store.AuditSystem(ctx, fmt.Sprintf("action.%03d", i), "ok", fmt.Sprintf("detail %d", i))
		if err != nil {
			t.Fatalf("insert audit %d: %v", i, err)
		}
	}

	page1, hasMore, err := store.AuditPaged(ctx, 50, 0)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1) != 50 {
		t.Fatalf("page 1 len = %d, want 50", len(page1))
	}
	if !hasMore {
		t.Fatalf("page 1 hasMore = false, want true")
	}

	lastID1 := page1[49].ID
	if lastID1 <= 0 {
		t.Fatalf("page 1 last ID = %d, want > 0", lastID1)
	}

	page2, hasMore, err := store.AuditPaged(ctx, 50, lastID1)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2) != 50 {
		t.Fatalf("page 2 len = %d, want 50", len(page2))
	}
	if !hasMore {
		t.Fatalf("page 2 hasMore = false, want true")
	}

	lastID2 := page2[49].ID
	page3, hasMore, err := store.AuditPaged(ctx, 50, lastID2)
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(page3) != 20 {
		t.Fatalf("page 3 len = %d, want 20", len(page3))
	}
	if hasMore {
		t.Fatalf("page 3 hasMore = true, want false")
	}

	lastID3 := page3[19].ID
	page4, hasMore, err := store.AuditPaged(ctx, 50, lastID3)
	if err != nil {
		t.Fatalf("page 4: %v", err)
	}
	if len(page4) != 0 {
		t.Fatalf("page 4 len = %d, want 0", len(page4))
	}
	if hasMore {
		t.Fatalf("page 4 hasMore = true, want false")
	}

	seen := make(map[int64]bool)
	all := append(append(page1, page2...), page3...)
	if len(all) != 120 {
		t.Fatalf("total items = %d, want 120", len(all))
	}
	for i, a := range all {
		if a.ID <= 0 {
			t.Fatalf("item %d has invalid ID %d", i, a.ID)
		}
		if seen[a.ID] {
			t.Fatalf("duplicate audit ID %d", a.ID)
		}
		seen[a.ID] = true
		if i > 0 && a.ID >= all[i-1].ID {
			t.Fatalf("IDs not in descending order: %d >= %d", a.ID, all[i-1].ID)
		}
	}
}
