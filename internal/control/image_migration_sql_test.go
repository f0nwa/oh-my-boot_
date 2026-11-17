package control

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestImageMigrationSQL(t *testing.T) {
	store := installationTestStore(t)
	pool := store.Pool
	ctx := context.Background()

	migration, err := os.ReadFile("../../deploy/sql/2026-11-18-image-migration.sql")
	if err != nil {
		t.Fatalf("failed to read migration: %v", err)
	}

	// Must be idempotent (apply twice)
	for i := range 2 {
		if _, err := pool.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("iteration %d: migration execution failed: %v", i+1, err)
		}
	}

	// 1. Verify image_migrations table exists and accepts records
	_, err = pool.Exec(ctx, `
		INSERT INTO image_migrations (id, source_storage_id, path, target_storage_id, state, total_bytes, copied_bytes)
		VALUES ('test-mig-1', 1, 'win.iso', 2, 'copying', 1000, 500)
		ON CONFLICT (id) DO UPDATE SET copied_bytes = EXCLUDED.copied_bytes
	`)
	if err != nil {
		t.Fatalf("failed to insert into image_migrations: %v", err)
	}

	// 2. Verify ubuntu_sources allows updating storage_id
	var bootProfileID int64
	err = pool.QueryRow(ctx, "INSERT INTO boot_profiles (kind, title) VALUES ('ubuntu_iso', 'Test Ubuntu') RETURNING id").Scan(&bootProfileID)
	if err != nil {
		t.Fatalf("failed to create test boot profile: %v", err)
	}

	sha := strings.Repeat("a", 64)
	var sourceID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO ubuntu_sources (boot_profile_id, storage_id, path, sha256, state)
		VALUES ($1, 10, 'ubuntu.iso', $2, 'ready')
		RETURNING id
	`, bootProfileID, sha).Scan(&sourceID)
	if err != nil {
		t.Fatalf("failed to insert test ubuntu source: %v", err)
	}

	// Update storage_id must succeed
	if _, err := pool.Exec(ctx, "UPDATE ubuntu_sources SET storage_id = 20 WHERE id = $1", sourceID); err != nil {
		t.Fatalf("updating storage_id on ubuntu_sources failed: %v", err)
	}

	// Updating sha256 or path must still be rejected by trigger
	if _, err := pool.Exec(ctx, "UPDATE ubuntu_sources SET sha256 = $1 WHERE id = $2", strings.Repeat("b", 64), sourceID); err == nil {
		t.Fatalf("expected trigger to reject sha256 update, got nil")
	}
}
