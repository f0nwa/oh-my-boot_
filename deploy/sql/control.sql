CREATE TABLE IF NOT EXISTS users (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 username TEXT NOT NULL,
 password_hash TEXT NOT NULL,
 role TEXT NOT NULL CHECK (role IN ('admin','operator','observer')),
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_idx ON users (lower(username));

CREATE TABLE IF NOT EXISTS sessions (
 token_hash TEXT PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE IF NOT EXISTS audit_events (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 actor_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
 action TEXT NOT NULL,
 result TEXT NOT NULL,
 detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS audit_events_occurred_at_idx ON audit_events (occurred_at DESC);

CREATE TABLE IF NOT EXISTS menu_versions (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 author_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
 title TEXT NOT NULL,
 diagnostic_label TEXT NOT NULL,
 diagnostic_message TEXT NOT NULL,
 script TEXT NOT NULL,
 sha256 TEXT NOT NULL,
 active BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE UNIQUE INDEX IF NOT EXISTS menu_versions_one_active_idx ON menu_versions (active) WHERE active;

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

CREATE TABLE IF NOT EXISTS windows_answer_variants (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 profile_id BIGINT NOT NULL REFERENCES windows_profiles(id) ON DELETE RESTRICT,
 title TEXT NOT NULL,
 folder TEXT NOT NULL DEFAULT '',
 sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
 size_bytes BIGINT NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 1048576),
 state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','ready','error')),
 last_error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS windows_answer_variants_profile_idx ON windows_answer_variants (profile_id);
