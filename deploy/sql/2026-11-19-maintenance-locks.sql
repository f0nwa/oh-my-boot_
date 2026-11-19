-- Maintenance mode (Drain) locks for storages and individual ISO images.
CREATE TABLE IF NOT EXISTS maintenance_locks (
 id BIGSERIAL PRIMARY KEY,
 scope TEXT NOT NULL CHECK (scope IN ('storage', 'image')),
 storage_id BIGINT NOT NULL CHECK (storage_id > 0),
 image_path TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT '',
 created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CONSTRAINT maintenance_locks_scope_path_check CHECK (
  (scope = 'storage' AND image_path = '') OR
  (scope = 'image' AND image_path <> '')
 ),
 UNIQUE (scope, storage_id, image_path)
);

DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'omb-control') THEN
  ALTER TABLE maintenance_locks OWNER TO "omb-control";
  ALTER SEQUENCE maintenance_locks_id_seq OWNER TO "omb-control";
 END IF;
END $$;
