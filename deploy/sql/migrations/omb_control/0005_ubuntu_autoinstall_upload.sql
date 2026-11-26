-- Support uploading custom Ubuntu Autoinstall YAML (user-data) files alongside constructor-generated ones.
ALTER TABLE ubuntu_autoinstall_files
 ADD COLUMN IF NOT EXISTS source_kind TEXT NOT NULL DEFAULT 'editor';

DO $$
DECLARE
 c RECORD;
BEGIN
 FOR c IN
  SELECT conname
  FROM pg_constraint
  WHERE conrelid = 'ubuntu_autoinstall_files'::regclass
    AND contype = 'c'
    AND (
     pg_get_constraintdef(oid) LIKE '%disk_mode%'
     OR pg_get_constraintdef(oid) LIKE '%DiskMode%'
     OR pg_get_constraintdef(oid) LIKE '%user_data%'
     OR pg_get_constraintdef(oid) LIKE '%source_kind%'
    )
 LOOP
  EXECUTE format('ALTER TABLE ubuntu_autoinstall_files DROP CONSTRAINT %I', c.conname);
 END LOOP;
END
$$;

ALTER TABLE ubuntu_autoinstall_files
 ADD CONSTRAINT ubuntu_autoinstall_files_source_kind_check CHECK (source_kind IN ('editor','uploaded')),
 ADD CONSTRAINT ubuntu_autoinstall_files_user_data_check CHECK (octet_length(user_data) > 0 AND octet_length(user_data) <= 65536),
 ADD CONSTRAINT ubuntu_autoinstall_files_disk_mode_check CHECK (disk_mode IN ('interactive','default','serial','custom','unknown')),
 ADD CONSTRAINT ubuntu_autoinstall_files_disk_serial_check CHECK ((disk_mode IN ('interactive','default','custom','unknown') AND disk_serial = '') OR (disk_mode = 'serial' AND octet_length(disk_serial) BETWEEN 1 AND 128)),
 ADD CONSTRAINT ubuntu_autoinstall_files_values_match_check CHECK (
  (source_kind = 'editor' AND "values"->>'DiskMode' = disk_mode AND "values"->>'DiskSerial' = disk_serial)
  OR (source_kind = 'uploaded' AND "values" = '{}'::jsonb)
 );

CREATE OR REPLACE FUNCTION reject_ubuntu_autoinstall_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id IS DISTINCT FROM OLD.id
    OR NEW.source_kind IS DISTINCT FROM OLD.source_kind
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
