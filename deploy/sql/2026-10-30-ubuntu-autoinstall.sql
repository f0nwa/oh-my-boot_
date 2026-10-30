-- Additive and idempotent: preserve all library, installation and menu history.
BEGIN;

CREATE TABLE IF NOT EXISTS ubuntu_autoinstall_files (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 title TEXT NOT NULL CHECK (btrim(title) <> ''),
 "values" JSONB NOT NULL CHECK (jsonb_typeof("values") = 'object' AND NOT ("values" ? 'Password')),
 user_data BYTEA NOT NULL CHECK (octet_length(user_data) > 0 AND octet_length(user_data) < 65536),
 sha256 CHAR(64) NOT NULL UNIQUE CHECK (sha256 ~ '^[0-9a-f]{64}$'),
 state TEXT NOT NULL CHECK (state = 'validated'),
 disk_mode TEXT NOT NULL CHECK (disk_mode IN ('interactive','serial')),
 disk_serial TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK ((disk_mode = 'interactive' AND disk_serial = '') OR (disk_mode = 'serial' AND octet_length(disk_serial) BETWEEN 1 AND 128)),
 CHECK ("values"->>'DiskMode' = disk_mode AND "values"->>'DiskSerial' = disk_serial)
);

ALTER TABLE installations ADD COLUMN IF NOT EXISTS autoinstall_id BIGINT REFERENCES ubuntu_autoinstall_files(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS installations_autoinstall_idx ON installations (autoinstall_id) WHERE autoinstall_id IS NOT NULL;

CREATE OR REPLACE FUNCTION reject_ubuntu_autoinstall_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Ubuntu Autoinstall files are immutable';
END;
$$;
DROP TRIGGER IF EXISTS ubuntu_autoinstall_immutable ON ubuntu_autoinstall_files;
CREATE TRIGGER ubuntu_autoinstall_immutable BEFORE UPDATE ON ubuntu_autoinstall_files FOR EACH ROW EXECUTE FUNCTION reject_ubuntu_autoinstall_update();

CREATE OR REPLACE FUNCTION reject_autoinstall_binding_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.autoinstall_id IS DISTINCT FROM OLD.autoinstall_id THEN
  RAISE EXCEPTION 'Installation Autoinstall bindings are immutable';
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS installation_autoinstall_immutable ON installations;
CREATE TRIGGER installation_autoinstall_immutable BEFORE UPDATE OF autoinstall_id ON installations FOR EACH ROW EXECUTE FUNCTION reject_autoinstall_binding_update();

COMMIT;
