BEGIN;
-- Historical discovery times were not recorded. Keep them unknown instead of
-- presenting the migration time as the date the ISO was added.
ALTER TABLE images ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMPTZ;
ALTER TABLE images ALTER COLUMN first_seen_at SET DEFAULT now();
COMMIT;
