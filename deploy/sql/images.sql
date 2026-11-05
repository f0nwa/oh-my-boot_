CREATE TABLE IF NOT EXISTS storages (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 name TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK (kind IN ('local','mounted','smb')),
 root TEXT NOT NULL UNIQUE,
 storage_key TEXT UNIQUE,
 server TEXT NOT NULL DEFAULT '',
 share TEXT NOT NULL DEFAULT '',
 subpath TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT 'unknown' CHECK (state IN ('unknown','scanning','ready','unavailable')),
 last_error TEXT NOT NULL DEFAULT '',
 last_scan_at TIMESTAMPTZ
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
