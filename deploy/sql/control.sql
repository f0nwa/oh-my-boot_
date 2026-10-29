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

ALTER TABLE users ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT 'ru' CHECK (language IN ('ru','en'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS theme TEXT NOT NULL DEFAULT 'system' CHECK (theme IN ('system','light','dark'));

ALTER TABLE windows_answer_variants ADD COLUMN IF NOT EXISTS wipes_disk BOOLEAN;
CREATE TABLE IF NOT EXISTS menu_draft (id SMALLINT PRIMARY KEY CHECK (id = 1), title TEXT NOT NULL, label TEXT NOT NULL, message TEXT NOT NULL, ubuntu BOOLEAN NOT NULL, ubuntu_folder TEXT NOT NULL DEFAULT '', local_timeout INT NOT NULL DEFAULT 10 CHECK (local_timeout BETWEEN 1 AND 3600), local_timer BOOLEAN NOT NULL DEFAULT true, diagnostics_on BOOLEAN NOT NULL DEFAULT true, updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS menu_draft_items (position INT PRIMARY KEY, key TEXT NOT NULL UNIQUE CHECK (key ~ '^win(iso|answer|driver)_[0-9]+$'), folder TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '');

CREATE TABLE IF NOT EXISTS machines (
    mac TEXT PRIMARY KEY CHECK (mac ~ '^[0-9a-f]{2}(:[0-9a-f]{2}){5}$'),
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen TIMESTAMPTZ NOT NULL,
    last_ip TEXT NOT NULL DEFAULT '',
    firmware TEXT NOT NULL DEFAULT '',
    manufacturer TEXT NOT NULL DEFAULT '',
    product TEXT NOT NULL DEFAULT '',
    serial TEXT NOT NULL DEFAULT '',
    uuid TEXT NOT NULL DEFAULT '',
    detected_at TIMESTAMPTZ,
    name TEXT NOT NULL DEFAULT '',
    manufacturer_override TEXT NOT NULL DEFAULT '',
    product_override TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS machines_last_seen_idx ON machines (last_seen DESC);
ALTER TABLE machines ADD COLUMN IF NOT EXISTS manual BOOLEAN NOT NULL DEFAULT false;
CREATE TABLE IF NOT EXISTS mac_prefix_rules (
    prefix TEXT PRIMARY KEY CHECK (prefix ~ '^[0-9a-f]{2}(:[0-9a-f]{2}){2,4}$'),
    title TEXT NOT NULL CHECK (title <> ''),
    manufacturer TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS winpe_driver_packages (
 sha256 TEXT PRIMARY KEY CHECK (sha256 ~ '^[0-9a-f]{64}$'),
 inventory JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS winpe_driver_configs (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 profile_id BIGINT NOT NULL REFERENCES windows_profiles(id) ON DELETE RESTRICT,
 answer_id BIGINT REFERENCES windows_answer_variants(id) ON DELETE RESTRICT,
 title TEXT NOT NULL,
 folder TEXT NOT NULL DEFAULT '',
 selected_infs JSONB NOT NULL CHECK (jsonb_typeof(selected_infs) = 'array' AND jsonb_array_length(selected_infs) BETWEEN 1 AND 128),
 state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','preparing','ready','error')),
 wim_key TEXT NOT NULL DEFAULT '',
 wim_sha256 TEXT NOT NULL DEFAULT '',
 script_sha256 TEXT NOT NULL DEFAULT '',
 last_error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS winpe_driver_configs_profile_idx ON winpe_driver_configs (profile_id);

CREATE TABLE IF NOT EXISTS boot_profiles (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 kind TEXT NOT NULL CHECK (kind IN ('windows_iso','ubuntu_builtin')),
 title TEXT NOT NULL CHECK (btrim(title) <> '')
);
CREATE UNIQUE INDEX IF NOT EXISTS boot_profiles_one_ubuntu_idx ON boot_profiles (kind) WHERE kind='ubuntu_builtin';

CREATE TABLE IF NOT EXISTS windows_boot_profiles (
 boot_profile_id BIGINT PRIMARY KEY REFERENCES boot_profiles(id) ON DELETE CASCADE,
 windows_profile_id BIGINT NOT NULL UNIQUE REFERENCES windows_profiles(id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS installations (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 boot_profile_id BIGINT NOT NULL REFERENCES boot_profiles(id) ON DELETE RESTRICT,
 title TEXT NOT NULL CHECK (btrim(title) <> ''),
 folder TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS installations_boot_profile_idx ON installations (boot_profile_id);

CREATE TABLE IF NOT EXISTS windows_installation_options (
 installation_id BIGINT PRIMARY KEY REFERENCES installations(id) ON DELETE CASCADE,
 answer_id BIGINT REFERENCES windows_answer_variants(id) ON DELETE RESTRICT,
 driver_config_id BIGINT REFERENCES winpe_driver_configs(id) ON DELETE RESTRICT,
 CHECK (answer_id IS NULL OR driver_config_id IS NULL)
);
