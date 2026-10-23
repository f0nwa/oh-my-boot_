-- Machines added by hand by MAC are marked until they first boot.
ALTER TABLE machines ADD COLUMN IF NOT EXISTS manual BOOLEAN NOT NULL DEFAULT false;
