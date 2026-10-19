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
