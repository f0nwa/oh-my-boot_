BEGIN;
CREATE TABLE IF NOT EXISTS image_inspections (
 storage_id BIGINT NOT NULL CHECK (storage_id > 0),
 path TEXT NOT NULL,
 storage_root TEXT NOT NULL,
 size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
 modified_at TIMESTAMPTZ NOT NULL,
 kind TEXT NOT NULL CHECK (kind IN ('windows','strelec','ubuntu','unsupported','error')),
 PRIMARY KEY (storage_id,path)
);
COMMIT;
