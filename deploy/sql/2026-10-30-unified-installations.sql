-- One-time stand import. Run before the new Control starts. No boot files or
-- Windows library objects are removed. The approved menu transition resets
-- the stand draft/history. The transaction and schema guard make reruns safe.
BEGIN;
SELECT pg_advisory_xact_lock(724330);
DO $migration$
DECLARE
 source RECORD;
 installation_id BIGINT;
BEGIN
 IF to_regclass('installations') IS NOT NULL THEN
  RETURN;
 END IF;
CREATE TABLE IF NOT EXISTS boot_profiles (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 kind TEXT NOT NULL CHECK (kind IN ('windows_iso','ubuntu_builtin')),
 title TEXT NOT NULL CHECK (btrim(title) <> '')
);
CREATE UNIQUE INDEX IF NOT EXISTS boot_profiles_one_ubuntu_idx ON boot_profiles (kind) WHERE kind='ubuntu_builtin';

CREATE TABLE IF NOT EXISTS windows_boot_profiles (
 boot_profile_id BIGINT PRIMARY KEY REFERENCES boot_profiles(id) ON DELETE CASCADE,
 windows_profile_id BIGINT NOT NULL UNIQUE REFERENCES windows_profiles(id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS installations (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 boot_profile_id BIGINT NOT NULL REFERENCES boot_profiles(id) ON DELETE RESTRICT,
 title TEXT NOT NULL CHECK (btrim(title) <> ''),
 folder TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS installations_boot_profile_idx ON installations (boot_profile_id);

CREATE TABLE IF NOT EXISTS windows_installation_options (
 installation_id BIGINT PRIMARY KEY REFERENCES installations(id) ON DELETE CASCADE,
 answer_id BIGINT REFERENCES windows_answer_variants(id) ON DELETE RESTRICT,
 driver_config_id BIGINT REFERENCES winpe_driver_configs(id) ON DELETE RESTRICT,
 CHECK (answer_id IS NULL OR driver_config_id IS NULL)
);

 -- Titles need not be unique; link every common profile to its exact ISO.
 FOR source IN SELECT id,title FROM windows_profiles ORDER BY id LOOP
  INSERT INTO boot_profiles(kind,title) VALUES('windows_iso',source.title) RETURNING id INTO installation_id;
  INSERT INTO windows_boot_profiles(boot_profile_id,windows_profile_id) VALUES(installation_id,source.id);
 END LOOP;
 INSERT INTO boot_profiles(kind,title) VALUES('ubuntu_builtin','Ubuntu Desktop');

 FOR source IN SELECT w.boot_profile_id,p.title,p.folder FROM windows_profiles p JOIN windows_boot_profiles w ON w.windows_profile_id=p.id ORDER BY p.id LOOP
  INSERT INTO installations(boot_profile_id,title,folder) VALUES(source.boot_profile_id,source.title,source.folder) RETURNING id INTO installation_id;
  INSERT INTO windows_installation_options(installation_id) VALUES(installation_id);
 END LOOP;
 FOR source IN SELECT w.boot_profile_id,a.id,a.title,a.folder FROM windows_answer_variants a JOIN windows_boot_profiles w ON w.windows_profile_id=a.profile_id ORDER BY a.id LOOP
  INSERT INTO installations(boot_profile_id,title,folder) VALUES(source.boot_profile_id,source.title,source.folder) RETURNING id INTO installation_id;
  INSERT INTO windows_installation_options(installation_id,answer_id) VALUES(installation_id,source.id);
 END LOOP;
 FOR source IN SELECT w.boot_profile_id,c.id,c.title,c.folder FROM winpe_driver_configs c JOIN windows_boot_profiles w ON w.windows_profile_id=c.profile_id ORDER BY c.id LOOP
  INSERT INTO installations(boot_profile_id,title,folder) VALUES(source.boot_profile_id,source.title,source.folder) RETURNING id INTO installation_id;
  INSERT INTO windows_installation_options(installation_id,driver_config_id) VALUES(installation_id,source.id);
 END LOOP;
 INSERT INTO installations(boot_profile_id,title,folder)
 SELECT id,'Ubuntu Desktop','' FROM boot_profiles WHERE kind='ubuntu_builtin';
 -- Old scripts cannot provide the new installation IDs. Reset only the
 -- approved draft/history; Boot keeps its current script until publication.
 DROP TABLE menu_draft_items;
 DELETE FROM menu_draft;
 DELETE FROM menu_versions;
 ALTER TABLE menu_versions ADD COLUMN IF NOT EXISTS diagnostics_on BOOLEAN NOT NULL DEFAULT true;
 ALTER TABLE menu_versions ADD COLUMN IF NOT EXISTS local_timeout INT NOT NULL DEFAULT 10 CHECK (local_timeout BETWEEN 1 AND 3600);
 ALTER TABLE menu_versions ADD COLUMN IF NOT EXISTS local_timer BOOLEAN NOT NULL DEFAULT true;
 ALTER TABLE menu_versions ADD COLUMN IF NOT EXISTS look TEXT NOT NULL DEFAULT '';
-- key is a temporary bridge removed when the editor uses installation IDs.
CREATE TABLE IF NOT EXISTS menu_draft_items (
 position INT PRIMARY KEY CHECK (position >= 0),
 installation_id BIGINT UNIQUE REFERENCES installations(id) ON DELETE RESTRICT,
 key TEXT UNIQUE CHECK (key ~ '^win(iso|answer|driver)_[0-9]+$'),
 folder TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL DEFAULT '',
 CHECK ((installation_id IS NULL) <> (key IS NULL))
);

CREATE TABLE IF NOT EXISTS menu_version_items (
 version_id BIGINT NOT NULL REFERENCES menu_versions(id) ON DELETE CASCADE,
 installation_id BIGINT NOT NULL REFERENCES installations(id) ON DELETE RESTRICT,
 position INT NOT NULL CHECK (position >= 0),
 title TEXT NOT NULL DEFAULT '',
 folder TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (version_id,position),
 UNIQUE (version_id,installation_id)
);
END
$migration$;
COMMIT;
