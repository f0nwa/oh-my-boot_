-- Existing drafts keep Diagnostics visible until the operator turns it off.
ALTER TABLE menu_draft ADD COLUMN IF NOT EXISTS diagnostics_on BOOLEAN NOT NULL DEFAULT true;
