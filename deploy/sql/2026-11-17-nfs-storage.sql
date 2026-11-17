BEGIN;

ALTER TABLE storages DROP CONSTRAINT IF EXISTS storages_kind_check;
ALTER TABLE storages ADD CONSTRAINT storages_kind_check CHECK (kind IN ('local','mounted','smb','nfs'));

ALTER TABLE storages ADD COLUMN IF NOT EXISTS export_path TEXT NOT NULL DEFAULT '';
ALTER TABLE storages ADD COLUMN IF NOT EXISTS nfs_version TEXT NOT NULL DEFAULT '';
ALTER TABLE storages DROP CONSTRAINT IF EXISTS storages_nfs_version_check;
ALTER TABLE storages ADD CONSTRAINT storages_nfs_version_check CHECK (nfs_version IN ('','4.1','3'));
ALTER TABLE storages ADD COLUMN IF NOT EXISTS uploads_enabled BOOLEAN NOT NULL DEFAULT FALSE;

COMMIT;
