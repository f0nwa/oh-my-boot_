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
