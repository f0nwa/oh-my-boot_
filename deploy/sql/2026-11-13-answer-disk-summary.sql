-- Description only: preserve answer contents, identities, dates and menu history.
ALTER TABLE windows_answer_variants ADD COLUMN IF NOT EXISTS disk_summary JSONB NOT NULL DEFAULT '{}'::jsonb;
