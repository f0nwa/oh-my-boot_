-- Local two-factor authentication (TOTP RFC 6238) and single-use recovery codes.
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret_enc BYTEA;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_last_step BIGINT NOT NULL DEFAULT 0;

ALTER TABLE password_policy ADD COLUMN IF NOT EXISTS totp_policy TEXT NOT NULL DEFAULT 'off';
ALTER TABLE password_policy DROP CONSTRAINT IF EXISTS password_policy_totp_policy_check;
ALTER TABLE password_policy ADD CONSTRAINT password_policy_totp_policy_check
	CHECK (totp_policy IN ('off', 'admins', 'all'));

CREATE TABLE IF NOT EXISTS user_recovery_codes (
	id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	code_hash TEXT NOT NULL CHECK (code_hash ~ '^[0-9a-f]{64}$'),
	used_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS user_recovery_codes_user_idx ON user_recovery_codes (user_id) WHERE used_at IS NULL;

CREATE TABLE IF NOT EXISTS login_2fa_challenges (
	token_hash TEXT PRIMARY KEY,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at TIMESTAMPTZ NOT NULL,
	attempts INT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS login_2fa_challenges_expires_idx ON login_2fa_challenges (expires_at);
