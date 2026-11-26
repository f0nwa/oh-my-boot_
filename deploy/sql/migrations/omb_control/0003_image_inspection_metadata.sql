-- Persist ISO version, architecture, summary, and WIM metadata from ISO inspector.

ALTER TABLE image_inspections
    ADD COLUMN IF NOT EXISTS version TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS arch TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS summary TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS wim_info JSONB NOT NULL DEFAULT '{}'::jsonb;
