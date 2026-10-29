-- Wait before the boot menu's main screen boots the local disk, in seconds,
-- and whether it does so at all. Existing drafts keep today's 10 seconds.
ALTER TABLE menu_draft ADD COLUMN IF NOT EXISTS local_timeout INT NOT NULL DEFAULT 10 CHECK (local_timeout BETWEEN 1 AND 3600);
ALTER TABLE menu_draft ADD COLUMN IF NOT EXISTS local_timer BOOLEAN NOT NULL DEFAULT true;
