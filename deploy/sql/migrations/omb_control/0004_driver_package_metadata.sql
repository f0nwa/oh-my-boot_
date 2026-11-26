-- Add human-readable title and description to WinPE driver packages.

ALTER TABLE winpe_driver_packages
    ADD COLUMN IF NOT EXISTS title TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
