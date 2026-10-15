ALTER TABLE storages DROP CONSTRAINT storages_kind_check;
ALTER TABLE storages ADD CONSTRAINT storages_kind_check CHECK (kind IN ('local','mounted','smb'));
ALTER TABLE storages ADD COLUMN storage_key TEXT UNIQUE;
ALTER TABLE storages ADD COLUMN server TEXT NOT NULL DEFAULT '';
ALTER TABLE storages ADD COLUMN share TEXT NOT NULL DEFAULT '';
ALTER TABLE storages ADD COLUMN subpath TEXT NOT NULL DEFAULT '';
