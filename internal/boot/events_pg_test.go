package boot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func bootTestStore(t *testing.T) PostgresStore {
	t.Helper()
	for _, binary := range []string{"initdb", "pg_ctl"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("PostgreSQL integration requires %s", binary)
		}
	}
	root, err := os.MkdirTemp("/tmp", "omb-boot-events-")
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

	sql, err := os.ReadFile("../../deploy/sql/boot.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatal(err)
	}
	transferSQL, err := os.ReadFile("../../deploy/sql/2026-10-23-boot-transfer.sql")
	if err == nil {
		_, _ = pool.Exec(context.Background(), string(transferSQL))
	}
	return PostgresStore{Pool: pool}
}

func TestPostgresStoreRecentPaged(t *testing.T) {
	store := bootTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 120; i++ {
		ts := base.Add(time.Duration(i/2) * time.Second)
		mac := "aa:bb:cc:dd:ee:01"
		if i%2 == 1 {
			mac = "aa:bb:cc:dd:ee:02"
		}
		err := store.Insert(ctx, Event{
			Kind:       "asset",
			Path:       fmt.Sprintf("/test-%03d", i),
			RemoteAddr: "192.168.1.100:1234",
			MAC:        mac,
			At:         ts,
			Status:     200,
			Bytes:      1024,
			Size:       1024,
		})
		if err != nil {
			t.Fatalf("insert event %d: %v", i, err)
		}
	}

	page1, err := store.RecentPaged(50, nil, 0, "")
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1) != 50 {
		t.Fatalf("page 1 len = %d, want 50", len(page1))
	}

	cursor1At := page1[49].At
	cursor1ID := page1[49].ID
	if cursor1ID <= 0 {
		t.Fatalf("page 1 last event ID = %d, want > 0", cursor1ID)
	}

	page2, err := store.RecentPaged(50, &cursor1At, cursor1ID, "")
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2) != 50 {
		t.Fatalf("page 2 len = %d, want 50", len(page2))
	}

	cursor2At := page2[49].At
	cursor2ID := page2[49].ID
	page3, err := store.RecentPaged(50, &cursor2At, cursor2ID, "")
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(page3) != 20 {
		t.Fatalf("page 3 len = %d, want 20", len(page3))
	}

	cursor3At := page3[19].At
	cursor3ID := page3[19].ID
	page4, err := store.RecentPaged(50, &cursor3At, cursor3ID, "")
	if err != nil {
		t.Fatalf("page 4: %v", err)
	}
	if len(page4) != 0 {
		t.Fatalf("page 4 len = %d, want 0", len(page4))
	}

	seen := make(map[int64]bool)
	all := append(append(page1, page2...), page3...)
	if len(all) != 120 {
		t.Fatalf("total items = %d, want 120", len(all))
	}
	for _, e := range all {
		if e.ID <= 0 {
			t.Fatalf("event ID not populated: %+v", e)
		}
		if seen[e.ID] {
			t.Fatalf("duplicate event ID %d encountered", e.ID)
		}
		seen[e.ID] = true
	}

	for i := 1; i < len(all); i++ {
		prev := all[i-1]
		curr := all[i]
		if curr.At.After(prev.At) {
			t.Fatalf("order violation at %d: %v > %v", i, curr.At, prev.At)
		}
		if curr.At.Equal(prev.At) && curr.ID >= prev.ID {
			t.Fatalf("order ID violation at %d: %d >= %d for same time %v", i, curr.ID, prev.ID, curr.At)
		}
	}

	mac1Events, err := store.RecentPaged(100, nil, 0, "aa:bb:cc:dd:ee:01")
	if err != nil {
		t.Fatalf("mac filter: %v", err)
	}
	if len(mac1Events) != 60 {
		t.Fatalf("mac1 count = %d, want 60", len(mac1Events))
	}
	for _, e := range mac1Events {
		if e.MAC != "aa:bb:cc:dd:ee:01" {
			t.Fatalf("unexpected MAC: %s", e.MAC)
		}
	}
}
