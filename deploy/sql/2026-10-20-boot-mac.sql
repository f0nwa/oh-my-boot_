-- Client MAC addresses in the boot request log.
ALTER TABLE boot_events ADD COLUMN IF NOT EXISTS mac TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS boot_events_mac_idx ON boot_events (mac, occurred_at DESC);
