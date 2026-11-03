BEGIN;
ALTER TABLE windows_profiles ADD COLUMN IF NOT EXISTS boot_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE windows_profiles DROP CONSTRAINT IF EXISTS windows_profiles_boot_mode_check;
ALTER TABLE windows_profiles ADD CONSTRAINT windows_profiles_boot_mode_check CHECK (boot_mode IN ('','strelec'));
ALTER TABLE boot_profiles DROP CONSTRAINT IF EXISTS boot_profiles_kind_check;
ALTER TABLE boot_profiles ADD CONSTRAINT boot_profiles_kind_check CHECK (kind IN ('windows_iso','winpe_iso','ubuntu_builtin'));
COMMIT;
