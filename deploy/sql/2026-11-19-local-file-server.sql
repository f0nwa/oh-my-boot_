-- Local SMB and SFTP file server settings and user access permissions.
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

DO $$
BEGIN
	IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'omb-control') THEN
		ALTER TABLE file_server_settings OWNER TO "omb-control";
	END IF;
END $$;
