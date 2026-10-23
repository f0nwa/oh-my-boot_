-- How each boot file request ended: HTTP status, body bytes sent, file size.
ALTER TABLE boot_events ADD COLUMN IF NOT EXISTS status INTEGER NOT NULL DEFAULT 0;
ALTER TABLE boot_events ADD COLUMN IF NOT EXISTS bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE boot_events ADD COLUMN IF NOT EXISTS size BIGINT NOT NULL DEFAULT 0;
