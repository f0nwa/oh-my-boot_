ALTER TABLE images
 ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'iso' CHECK (kind IN ('iso','wim','esd'));

CREATE INDEX IF NOT EXISTS images_present_kind_idx ON images (storage_id, present, kind, path);
