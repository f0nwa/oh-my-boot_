CREATE TABLE IF NOT EXISTS storages (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 name TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK (kind IN ('local','mounted','smb','nfs')),
 root TEXT NOT NULL UNIQUE,
 storage_key TEXT UNIQUE,
 server TEXT NOT NULL DEFAULT '',
 share TEXT NOT NULL DEFAULT '',
 subpath TEXT NOT NULL DEFAULT '',
 export_path TEXT NOT NULL DEFAULT '',
 nfs_version TEXT NOT NULL DEFAULT '' CHECK (nfs_version IN ('','4.1','3')),
 uploads_enabled BOOLEAN NOT NULL DEFAULT FALSE,
 state TEXT NOT NULL DEFAULT 'unknown' CHECK (state IN ('unknown','scanning','ready','unavailable')),
 last_error TEXT NOT NULL DEFAULT '',
 last_scan_at TIMESTAMPTZ,
 other_bytes BIGINT NOT NULL DEFAULT 0 CHECK (other_bytes >= 0)
);

CREATE TABLE IF NOT EXISTS images (
 storage_id BIGINT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
 path TEXT NOT NULL,
 size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
 modified_at TIMESTAMPTZ NOT NULL,
 present BOOLEAN NOT NULL DEFAULT TRUE,
 first_seen_at TIMESTAMPTZ DEFAULT now(),
 PRIMARY KEY (storage_id, path)
);
CREATE INDEX IF NOT EXISTS images_present_idx ON images (storage_id, present, path);
