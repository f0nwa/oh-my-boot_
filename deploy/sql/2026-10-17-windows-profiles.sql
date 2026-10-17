CREATE TABLE IF NOT EXISTS windows_profiles (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 storage_id BIGINT NOT NULL CHECK (storage_id > 0),
 path TEXT NOT NULL,
 storage_root TEXT NOT NULL,
 size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
 modified_at TIMESTAMPTZ NOT NULL,
 title TEXT NOT NULL,
 folder TEXT NOT NULL DEFAULT '',
 install_file TEXT NOT NULL DEFAULT '' CHECK (install_file IN ('','install.wim','install.esd')),
 state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','preparing','ready','error','unavailable')),
 last_error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE (storage_id,path)
);
