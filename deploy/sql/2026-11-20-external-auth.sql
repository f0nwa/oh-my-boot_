-- Внешний вход: Active Directory / LDAP и Keycloak (OIDC).
-- Идемпотентная миграция существующей базы Control.

ALTER TABLE users ADD COLUMN IF NOT EXISTS auth_source TEXT NOT NULL DEFAULT 'local';
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_auth_source_check;
ALTER TABLE users ADD CONSTRAINT users_auth_source_check CHECK (auth_source IN ('local', 'ldap', 'keycloak'));

ALTER TABLE users ADD COLUMN IF NOT EXISTS external_id TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_password_hash_source_check;
ALTER TABLE users ADD CONSTRAINT users_password_hash_source_check CHECK (
	(auth_source = 'local' AND password_hash IS NOT NULL AND password_hash <> '') OR
	(auth_source <> 'local' AND password_hash IS NULL)
);

DROP INDEX IF EXISTS users_username_lower_idx;
CREATE UNIQUE INDEX IF NOT EXISTS users_local_username_lower_idx ON users (lower(username)) WHERE auth_source = 'local';
CREATE UNIQUE INDEX IF NOT EXISTS users_external_identity_idx ON users (auth_source, external_id) WHERE auth_source <> 'local';

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS auth_source TEXT NOT NULL DEFAULT 'local';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS external_sid TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS sessions_auth_source_idx ON sessions (auth_source);
CREATE INDEX IF NOT EXISTS sessions_external_sid_idx ON sessions (external_sid) WHERE external_sid <> '';

ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS actor_name TEXT NOT NULL DEFAULT '';
UPDATE audit_events a
SET actor_name = u.username
FROM users u
WHERE a.actor_id = u.id AND a.actor_name = '';

ALTER TABLE menu_versions ADD COLUMN IF NOT EXISTS author_name TEXT NOT NULL DEFAULT '';
UPDATE menu_versions m
SET author_name = u.username
FROM users u
WHERE m.author_id = u.id AND m.author_name = '';

ALTER TABLE system_events DROP CONSTRAINT IF EXISTS system_events_category_check;
ALTER TABLE system_events ADD CONSTRAINT system_events_category_check
	CHECK (category IN ('storage', 'service', 'network', 'task', 'auth'));

CREATE TABLE IF NOT EXISTS auth_integrations (
	provider TEXT PRIMARY KEY CHECK (provider IN ('ldap', 'keycloak')),
	enabled BOOLEAN NOT NULL DEFAULT FALSE,
	config_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	secrets_enc BYTEA,
	last_test_at TIMESTAMPTZ,
	last_test_ok BOOLEAN,
	last_test_detail TEXT NOT NULL DEFAULT '',
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS oidc_login_states (
	state TEXT PRIMARY KEY,
	nonce TEXT NOT NULL,
	code_verifier TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS oidc_login_states_expires_idx ON oidc_login_states (expires_at);

CREATE TABLE IF NOT EXISTS oidc_logout_jti (
	jti TEXT PRIMARY KEY,
	expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS oidc_logout_jti_expires_idx ON oidc_logout_jti (expires_at);
