-- Run only after the migration preflight has verified the ISO and manifest.
-- The caller supplies the verified catalog identity on this database session.
BEGIN;
SELECT pg_advisory_xact_lock(724330);

ALTER TABLE boot_profiles DROP CONSTRAINT IF EXISTS boot_profiles_kind_check;
ALTER TABLE boot_profiles ADD CONSTRAINT boot_profiles_kind_check CHECK (kind IN ('windows_iso','winpe_iso','ubuntu_builtin','ubuntu_iso'));
ALTER TABLE menu_versions ADD COLUMN IF NOT EXISTS legacy_ubuntu_source_id BIGINT;

DO $titles$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='menu_draft_items' AND column_name='title_override') THEN
  ALTER TABLE menu_draft_items ADD COLUMN title_override TEXT;
  UPDATE menu_draft_items d SET title_override=d.title FROM installations i
   WHERE i.id=d.installation_id AND d.title<>'' AND d.title<>i.title;
 END IF;
END
$titles$;

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
ALTER TABLE ubuntu_sources DROP CONSTRAINT IF EXISTS ubuntu_sources_state_check;
ALTER TABLE ubuntu_sources ADD CONSTRAINT ubuntu_sources_state_check CHECK (state IN ('unverified','checking','verified','preparing','ready','error'));
CREATE OR REPLACE FUNCTION reject_ubuntu_source_identity_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (OLD.boot_profile_id, OLD.storage_id, OLD.path, OLD.sha256) IS DISTINCT FROM (NEW.boot_profile_id, NEW.storage_id, NEW.path, NEW.sha256) THEN
  RAISE EXCEPTION 'Ubuntu source identity is immutable';
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS ubuntu_source_identity_immutable ON ubuntu_sources;
CREATE TRIGGER ubuntu_source_identity_immutable BEFORE UPDATE ON ubuntu_sources FOR EACH ROW EXECUTE FUNCTION reject_ubuntu_source_identity_update();

DO $migration$
DECLARE
 old_profile BIGINT;
 storage_text TEXT;
 source_path TEXT;
 source_sha TEXT;
BEGIN
 SELECT id INTO old_profile FROM boot_profiles WHERE kind='ubuntu_builtin';
 IF old_profile IS NOT NULL AND NOT EXISTS (SELECT 1 FROM ubuntu_sources WHERE boot_profile_id=old_profile) THEN
  storage_text := current_setting('omb.ubuntu_storage_id', true);
  source_path := current_setting('omb.ubuntu_path', true);
  source_sha := current_setting('omb.ubuntu_sha256', true);
  IF storage_text IS NULL OR storage_text !~ '^[1-9][0-9]*$' OR source_path IS NULL OR source_path = '' OR source_sha IS NULL OR source_sha !~ '^[0-9a-f]{64}$' THEN
   RAISE EXCEPTION 'verified legacy Ubuntu source identity is required';
  END IF;
  INSERT INTO ubuntu_sources(boot_profile_id,storage_id,path,sha256,state)
  VALUES(old_profile,storage_text::BIGINT,source_path,source_sha,'ready');
 END IF;
END
$migration$;

-- Only the active source has been attested at the moment of migration.
UPDATE menu_versions
SET legacy_ubuntu_source_id = (SELECT u.id FROM ubuntu_sources u JOIN boot_profiles b ON b.id=u.boot_profile_id WHERE b.kind='ubuntu_builtin')
WHERE active AND legacy_ubuntu_source_id IS NULL
  AND (position('/images/ubuntu-desktop.iso' in script)>0 OR position('/assets/ubuntu/vmlinuz' in script)>0 OR position('/assets/ubuntu/initrd' in script)>0 OR position('/assets/ubuntu/bootx64.efi' in script)>0);

COMMIT;
