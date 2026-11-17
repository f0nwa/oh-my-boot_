-- 2026-11-18-image-migration.sql: migration support for ISO images across storages.

-- 1. Allow updating storage_id during image migration while keeping boot_profile_id, path, and sha256 immutable.
CREATE OR REPLACE FUNCTION reject_ubuntu_source_identity_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (OLD.boot_profile_id, OLD.path, OLD.sha256) IS DISTINCT FROM (NEW.boot_profile_id, NEW.path, NEW.sha256) OR NEW.storage_id <= 0 THEN
  RAISE EXCEPTION 'Ubuntu source identity is immutable';
 END IF;
 RETURN NEW;
END;
$$;

-- 2. Persistence table for tracking active and historical image migrations.
CREATE TABLE IF NOT EXISTS image_migrations (
    id TEXT PRIMARY KEY,
    source_storage_id BIGINT NOT NULL CHECK (source_storage_id > 0),
    path TEXT NOT NULL,
    target_storage_id BIGINT NOT NULL CHECK (target_storage_id > 0),
    state TEXT NOT NULL CHECK (state IN ('copying', 'ready_to_rebind', 'rebinding', 'completed', 'failed', 'canceled')),
    total_bytes BIGINT NOT NULL DEFAULT 0,
    copied_bytes BIGINT NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE image_migrations ADD COLUMN IF NOT EXISTS user_id BIGINT REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS image_migrations_active_idx ON image_migrations (source_storage_id, path, state);

DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'omb-control') THEN
  GRANT ALL ON image_migrations TO "omb-control";
 END IF;
END $$;
