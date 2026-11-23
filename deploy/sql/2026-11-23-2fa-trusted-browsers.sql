-- Trusted browsers for two-factor authentication (2FA).
CREATE TABLE IF NOT EXISTS user_trusted_browsers (
	id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	token_hash TEXT NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
	user_agent TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	last_used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS user_trusted_browsers_user_idx ON user_trusted_browsers (user_id);
CREATE INDEX IF NOT EXISTS user_trusted_browsers_token_idx ON user_trusted_browsers (token_hash);
CREATE INDEX IF NOT EXISTS user_trusted_browsers_expires_idx ON user_trusted_browsers (expires_at);
