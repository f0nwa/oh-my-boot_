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
 active BOOLEAN NOT NULL DEFAULT FALSE,
 diagnostics_on BOOLEAN NOT NULL DEFAULT true,
 local_timeout INT NOT NULL DEFAULT 10 CHECK (local_timeout BETWEEN 1 AND 3600),
 local_timer BOOLEAN NOT NULL DEFAULT true,
 default_installation_id BIGINT NOT NULL DEFAULT 0,
 legacy_ubuntu_source_id BIGINT,
 look TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS menu_versions_one_active_idx ON menu_versions (active) WHERE active;

CREATE TABLE IF NOT EXISTS windows_profiles (
 boot_mode TEXT NOT NULL DEFAULT '' CHECK (boot_mode IN ('','strelec')),
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
 wim_info JSONB NOT NULL DEFAULT '{}'::jsonb,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE (storage_id,path)
);

CREATE TABLE IF NOT EXISTS windows_answer_variants (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 title TEXT NOT NULL,
 folder TEXT NOT NULL DEFAULT '',
 sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
 size_bytes BIGINT NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 1048576),
 state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','ready','error')),
 last_error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE users ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT 'ru' CHECK (language IN ('ru','en'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS theme TEXT NOT NULL DEFAULT 'system' CHECK (theme IN ('system','light','dark'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_changed_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE users ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_policy_mode TEXT NOT NULL DEFAULT 'policy' CHECK (password_policy_mode IN ('policy', 'never', 'custom'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_max_age_days INT CHECK (password_max_age_days IS NULL OR password_max_age_days BETWEEN 1 AND 3650);
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_password_policy_custom_check;
ALTER TABLE users ADD CONSTRAINT users_password_policy_custom_check CHECK (
 (password_policy_mode = 'custom' AND password_max_age_days BETWEEN 1 AND 3650)
 OR (password_policy_mode <> 'custom' AND password_max_age_days IS NULL)
);

CREATE TABLE IF NOT EXISTS password_policy (
 id SMALLINT PRIMARY KEY CHECK (id = 1),
 max_age_days INT NOT NULL DEFAULT 0 CHECK (max_age_days BETWEEN 0 AND 3650),
 warn_days INT NOT NULL DEFAULT 7 CHECK (warn_days BETWEEN 0 AND 90),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO password_policy (id, max_age_days, warn_days) VALUES (1, 0, 7) ON CONFLICT (id) DO NOTHING;

ALTER TABLE windows_profiles ADD COLUMN IF NOT EXISTS wim_info JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE windows_answer_variants ADD COLUMN IF NOT EXISTS wipes_disk BOOLEAN;
ALTER TABLE windows_answer_variants ADD COLUMN IF NOT EXISTS disk_summary JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE windows_answer_variants ADD COLUMN IF NOT EXISTS target_summary JSONB NOT NULL DEFAULT '{}'::jsonb;
DROP INDEX IF EXISTS windows_answer_variants_profile_idx;
ALTER TABLE windows_answer_variants DROP COLUMN IF EXISTS profile_id;
CREATE TABLE IF NOT EXISTS menu_draft (id SMALLINT PRIMARY KEY CHECK (id = 1), title TEXT NOT NULL, label TEXT NOT NULL, message TEXT NOT NULL, local_timeout INT NOT NULL DEFAULT 10 CHECK (local_timeout BETWEEN 1 AND 3600), local_timer BOOLEAN NOT NULL DEFAULT true, diagnostics_on BOOLEAN NOT NULL DEFAULT true, default_installation_id BIGINT NOT NULL DEFAULT 0, updated_at TIMESTAMPTZ NOT NULL DEFAULT now());

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
    network_driver TEXT NOT NULL DEFAULT '' CHECK (network_driver IN ('', 'ipxe', 'snp')),
    dhcp_ignored BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS machines_last_seen_idx ON machines (last_seen DESC);
ALTER TABLE machines ADD COLUMN IF NOT EXISTS manual BOOLEAN NOT NULL DEFAULT false;
CREATE TABLE IF NOT EXISTS mac_prefix_rules (
    prefix TEXT PRIMARY KEY CHECK (prefix ~ '^[0-9a-f]{2}(:[0-9a-f]{2}){2,4}$'),
    title TEXT NOT NULL CHECK (title <> ''),
    manufacturer TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    network_driver TEXT NOT NULL DEFAULT '' CHECK (network_driver IN ('', 'ipxe', 'snp')),
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
 author TEXT NOT NULL DEFAULT '',
 author_inferred BOOLEAN NOT NULL DEFAULT FALSE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE winpe_driver_configs ADD COLUMN IF NOT EXISTS author TEXT NOT NULL DEFAULT '';
ALTER TABLE winpe_driver_configs ADD COLUMN IF NOT EXISTS author_inferred BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS winpe_driver_configs_profile_idx ON winpe_driver_configs (profile_id);

CREATE TABLE IF NOT EXISTS boot_profiles (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 kind TEXT NOT NULL CHECK (kind IN ('windows_iso','winpe_iso','ubuntu_builtin','ubuntu_iso','windows_wim')),
 title TEXT NOT NULL CHECK (btrim(title) <> '')
);
CREATE UNIQUE INDEX IF NOT EXISTS boot_profiles_one_ubuntu_idx ON boot_profiles (kind) WHERE kind='ubuntu_builtin';

CREATE TABLE IF NOT EXISTS ubuntu_sources (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 boot_profile_id BIGINT NOT NULL UNIQUE REFERENCES boot_profiles(id) ON DELETE RESTRICT,
 storage_id BIGINT NOT NULL CHECK (storage_id > 0),
 path TEXT NOT NULL CHECK (path <> ''),
 sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
 state TEXT NOT NULL CHECK (state IN ('unverified','checking','verified','preparing','ready','error')),
 error TEXT NOT NULL DEFAULT '',
 UNIQUE (storage_id,path,sha256)
);
CREATE OR REPLACE FUNCTION reject_ubuntu_source_identity_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (OLD.boot_profile_id, OLD.path, OLD.sha256) IS DISTINCT FROM (NEW.boot_profile_id, NEW.path, NEW.sha256) OR NEW.storage_id <= 0 THEN
  RAISE EXCEPTION 'Ubuntu source identity is immutable';
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS ubuntu_source_identity_immutable ON ubuntu_sources;
CREATE TRIGGER ubuntu_source_identity_immutable BEFORE UPDATE ON ubuntu_sources FOR EACH ROW EXECUTE FUNCTION reject_ubuntu_source_identity_update();

CREATE TABLE IF NOT EXISTS windows_boot_profiles (
 boot_profile_id BIGINT PRIMARY KEY REFERENCES boot_profiles(id) ON DELETE CASCADE,
 windows_profile_id BIGINT NOT NULL UNIQUE REFERENCES windows_profiles(id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS ubuntu_autoinstall_files (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 title TEXT NOT NULL CHECK (btrim(title) <> ''),
 "values" JSONB NOT NULL CHECK (jsonb_typeof("values") = 'object' AND NOT ("values" ? 'Password')),
 user_data BYTEA NOT NULL CHECK (octet_length(user_data) > 0 AND octet_length(user_data) < 65536),
 sha256 CHAR(64) NOT NULL UNIQUE CHECK (sha256 ~ '^[0-9a-f]{64}$'),
 state TEXT NOT NULL CHECK (state = 'validated'),
 disk_mode TEXT NOT NULL CONSTRAINT ubuntu_autoinstall_files_disk_mode_check CHECK (disk_mode IN ('interactive','default','serial')),
 disk_serial TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CONSTRAINT ubuntu_autoinstall_files_disk_serial_check CHECK ((disk_mode IN ('interactive','default') AND disk_serial = '') OR (disk_mode = 'serial' AND octet_length(disk_serial) BETWEEN 1 AND 128)),
 CHECK ("values"->>'DiskMode' = disk_mode AND "values"->>'DiskSerial' = disk_serial)
);

CREATE TABLE IF NOT EXISTS installations (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 boot_profile_id BIGINT NOT NULL REFERENCES boot_profiles(id) ON DELETE RESTRICT,
 title TEXT NOT NULL CHECK (btrim(title) <> ''),
 folder TEXT NOT NULL DEFAULT '',
 autoinstall_id BIGINT REFERENCES ubuntu_autoinstall_files(id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS installations_boot_profile_idx ON installations (boot_profile_id);

CREATE TABLE IF NOT EXISTS windows_installation_options (
 installation_id BIGINT PRIMARY KEY REFERENCES installations(id) ON DELETE CASCADE,
 answer_id BIGINT REFERENCES windows_answer_variants(id) ON DELETE RESTRICT,
 driver_config_id BIGINT REFERENCES winpe_driver_configs(id) ON DELETE RESTRICT,
 CHECK (answer_id IS NULL OR driver_config_id IS NULL)
);

CREATE TABLE IF NOT EXISTS menu_draft_items (
 position INT PRIMARY KEY CHECK (position >= 0),
 installation_id BIGINT NOT NULL UNIQUE REFERENCES installations(id) ON DELETE RESTRICT,
 folder TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL DEFAULT '',
 title_override TEXT,
 components JSONB
);

CREATE TABLE IF NOT EXISTS menu_version_items (
 version_id BIGINT NOT NULL REFERENCES menu_versions(id) ON DELETE CASCADE,
 -- History retains the entry identity after explicit deletion. New versions
 -- validate live entries transactionally in SaveActiveMenu.
 installation_id BIGINT NOT NULL,
 position INT NOT NULL CHECK (position >= 0),
 title TEXT NOT NULL DEFAULT '',
 folder TEXT NOT NULL DEFAULT '',
 components JSONB,
 PRIMARY KEY (version_id,position),
 UNIQUE (version_id,installation_id)
);

CREATE INDEX IF NOT EXISTS installations_autoinstall_idx ON installations (autoinstall_id) WHERE autoinstall_id IS NOT NULL;

CREATE OR REPLACE FUNCTION reject_ubuntu_autoinstall_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id IS DISTINCT FROM OLD.id
    OR NEW."values" IS DISTINCT FROM OLD."values"
    OR NEW.user_data IS DISTINCT FROM OLD.user_data
    OR NEW.sha256 IS DISTINCT FROM OLD.sha256
    OR NEW.state IS DISTINCT FROM OLD.state
    OR NEW.disk_mode IS DISTINCT FROM OLD.disk_mode
    OR NEW.disk_serial IS DISTINCT FROM OLD.disk_serial
    OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
  RAISE EXCEPTION 'Ubuntu Autoinstall files are immutable';
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS ubuntu_autoinstall_immutable ON ubuntu_autoinstall_files;
CREATE TRIGGER ubuntu_autoinstall_immutable BEFORE UPDATE ON ubuntu_autoinstall_files FOR EACH ROW EXECUTE FUNCTION reject_ubuntu_autoinstall_update();

-- Boot-entry bindings are editable; menu items retain their own component snapshot.
DROP TRIGGER IF EXISTS installation_autoinstall_immutable ON installations;
DROP FUNCTION IF EXISTS reject_autoinstall_binding_update();

CREATE TABLE IF NOT EXISTS image_names (
 storage_id BIGINT NOT NULL CHECK (storage_id > 0),
 path TEXT NOT NULL,
 name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
 PRIMARY KEY (storage_id, path)
);

CREATE TABLE IF NOT EXISTS image_inspections (
 storage_id BIGINT NOT NULL CHECK (storage_id > 0),
 path TEXT NOT NULL,
 storage_root TEXT NOT NULL,
 size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
 modified_at TIMESTAMPTZ NOT NULL,
 kind TEXT NOT NULL CHECK (kind IN ('windows','strelec','ubuntu','unsupported','error')),
 PRIMARY KEY (storage_id,path)
);
CREATE TABLE IF NOT EXISTS answer_installation_checks (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 windows_answer_id BIGINT REFERENCES windows_answer_variants(id) ON DELETE CASCADE,
 ubuntu_autoinstall_id BIGINT REFERENCES ubuntu_autoinstall_files(id) ON DELETE CASCADE,
 windows_profile_id BIGINT REFERENCES windows_profiles(id) ON DELETE SET NULL,
 profile_title TEXT NOT NULL DEFAULT '',
 sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
 result TEXT NOT NULL CHECK (result IN ('success','failure')),
 machine TEXT NOT NULL DEFAULT '' CHECK (char_length(machine) <= 200),
 comment TEXT NOT NULL DEFAULT '' CHECK (char_length(comment) <= 2000),
 user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
 author TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (num_nonnulls(windows_answer_id,ubuntu_autoinstall_id) = 1)
);
ALTER TABLE answer_installation_checks ADD COLUMN IF NOT EXISTS windows_profile_id BIGINT REFERENCES windows_profiles(id) ON DELETE SET NULL;
ALTER TABLE answer_installation_checks ADD COLUMN IF NOT EXISTS profile_title TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS answer_checks_windows_idx ON answer_installation_checks (windows_answer_id,id DESC) WHERE windows_answer_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS answer_checks_ubuntu_idx ON answer_installation_checks (ubuntu_autoinstall_id,id DESC) WHERE ubuntu_autoinstall_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS answer_checks_profile_idx ON answer_installation_checks (COALESCE(windows_answer_id,0), COALESCE(ubuntu_autoinstall_id,0), COALESCE(windows_profile_id,0), profile_title, id DESC);

CREATE TABLE IF NOT EXISTS image_migrations (
 id TEXT PRIMARY KEY,
 source_storage_id BIGINT NOT NULL CHECK (source_storage_id > 0),
 path TEXT NOT NULL,
 target_storage_id BIGINT NOT NULL CHECK (target_storage_id > 0),
 state TEXT NOT NULL CHECK (state IN ('copying', 'ready_to_rebind', 'rebinding', 'completed', 'failed', 'canceled')),
 total_bytes BIGINT NOT NULL DEFAULT 0,
 copied_bytes BIGINT NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '',
 user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS image_migrations_active_idx ON image_migrations (source_storage_id, path, state);

CREATE TABLE IF NOT EXISTS maintenance_locks (
 id BIGSERIAL PRIMARY KEY,
 scope TEXT NOT NULL CHECK (scope IN ('storage', 'image')),
 storage_id BIGINT NOT NULL CHECK (storage_id > 0),
 image_path TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT '',
 created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CONSTRAINT maintenance_locks_scope_path_check CHECK (
  (scope = 'storage' AND image_path = '') OR
  (scope = 'image' AND image_path <> '')
 ),
 UNIQUE (scope, storage_id, image_path)
);

CREATE TABLE IF NOT EXISTS system_events (
 id BIGSERIAL PRIMARY KEY,
 occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 severity TEXT NOT NULL CHECK (severity IN ('error', 'warning', 'info')),
 category TEXT NOT NULL CHECK (category IN ('storage', 'service', 'network', 'task')),
 source_key TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL,
 detail TEXT NOT NULL DEFAULT '',
 link TEXT NOT NULL DEFAULT '',
 resolved_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS system_events_occurred_idx ON system_events (occurred_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS system_events_severity_idx ON system_events (severity, occurred_at DESC);
CREATE INDEX IF NOT EXISTS system_events_active_idx ON system_events (severity, occurred_at DESC) WHERE resolved_at IS NULL;

ALTER TABLE users ADD COLUMN IF NOT EXISTS fs_access_mode TEXT NOT NULL DEFAULT 'none'
	CHECK (fs_access_mode IN ('none', 'ro', 'rw'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS fs_password_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS fs_password_changed_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS file_server_settings (
	id SMALLINT PRIMARY KEY CHECK (id = 1),
	smb_enabled BOOLEAN NOT NULL DEFAULT FALSE,
	sftp_enabled BOOLEAN NOT NULL DEFAULT FALSE,
	sftp_port INT NOT NULL DEFAULT 2022 CHECK (sftp_port BETWEEN 1024 AND 65535),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO file_server_settings (id, smb_enabled, sftp_enabled, sftp_port)
VALUES (1, FALSE, FALSE, 2022)
ON CONFLICT (id) DO NOTHING;

-- External authentication: Active Directory / LDAP and Keycloak (OIDC).
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
ALTER TABLE menu_versions ADD COLUMN IF NOT EXISTS author_name TEXT NOT NULL DEFAULT '';

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
	jti_hash TEXT PRIMARY KEY,
	expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS oidc_logout_jti_expires_idx ON oidc_logout_jti (expires_at);

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

-- Split Windows boot.wim and install.wim/.esd management and independent swap support.
CREATE TABLE IF NOT EXISTS windows_boot_images (
	id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	source_kind TEXT NOT NULL CHECK (source_kind IN ('iso', 'file')),
	windows_profile_id BIGINT UNIQUE REFERENCES windows_profiles(id) ON DELETE CASCADE,
	storage_id BIGINT CHECK (storage_id IS NULL OR storage_id > 0),
	path TEXT NOT NULL DEFAULT '',
	storage_root TEXT NOT NULL DEFAULT '',
	size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
	modified_at TIMESTAMPTZ,
	title TEXT NOT NULL CHECK (btrim(title) <> ''),
	sha256 TEXT NOT NULL DEFAULT '' CHECK (sha256 = '' OR sha256 ~ '^[0-9a-f]{64}$'),
	has_setup_exe BOOLEAN NOT NULL DEFAULT FALSE,
	wim_info JSONB NOT NULL DEFAULT '{}'::jsonb,
	state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'preparing', 'ready', 'error', 'unavailable')),
	last_error TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	CHECK (
		(source_kind = 'iso' AND windows_profile_id IS NOT NULL AND storage_id IS NULL AND path = '')
		OR (source_kind = 'file' AND windows_profile_id IS NULL AND storage_id IS NOT NULL AND btrim(path) <> '')
	)
);
CREATE UNIQUE INDEX IF NOT EXISTS windows_boot_images_file_uniq
	ON windows_boot_images (storage_id, path) WHERE source_kind = 'file';

CREATE TABLE IF NOT EXISTS windows_install_images (
	id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	storage_id BIGINT NOT NULL CHECK (storage_id > 0),
	path TEXT NOT NULL CHECK (btrim(path) <> ''),
	storage_root TEXT NOT NULL,
	install_file TEXT NOT NULL CHECK (install_file IN ('install.wim', 'install.esd')),
	size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
	modified_at TIMESTAMPTZ NOT NULL,
	title TEXT NOT NULL CHECK (btrim(title) <> ''),
	wim_info JSONB NOT NULL DEFAULT '{}'::jsonb,
	state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'preparing', 'ready', 'error', 'unavailable')),
	last_error TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (storage_id, path)
);

ALTER TABLE boot_profiles DROP CONSTRAINT IF EXISTS boot_profiles_kind_check;
ALTER TABLE boot_profiles ADD CONSTRAINT boot_profiles_kind_check
	CHECK (kind IN ('windows_iso', 'winpe_iso', 'ubuntu_builtin', 'ubuntu_iso', 'windows_wim'));

CREATE TABLE IF NOT EXISTS windows_install_boot_profiles (
	boot_profile_id BIGINT PRIMARY KEY REFERENCES boot_profiles(id) ON DELETE CASCADE,
	install_image_id BIGINT NOT NULL UNIQUE REFERENCES windows_install_images(id) ON DELETE RESTRICT
);

ALTER TABLE windows_installation_options
	ADD COLUMN IF NOT EXISTS boot_image_id BIGINT REFERENCES windows_boot_images(id) ON DELETE RESTRICT;
ALTER TABLE windows_installation_options
	ADD COLUMN IF NOT EXISTS install_image_id BIGINT REFERENCES windows_install_images(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS windows_installation_options_boot_image_idx
	ON windows_installation_options (boot_image_id) WHERE boot_image_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS windows_installation_options_install_image_idx
	ON windows_installation_options (install_image_id) WHERE install_image_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS schema_migrations (
    id TEXT PRIMARY KEY,
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

