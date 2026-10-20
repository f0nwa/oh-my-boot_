CREATE TABLE IF NOT EXISTS boot_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    kind TEXT NOT NULL,
    path TEXT NOT NULL,
    remote_addr TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS boot_events_occurred_at_idx ON boot_events (occurred_at DESC);

ALTER TABLE boot_events ADD COLUMN IF NOT EXISTS mac TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS boot_events_mac_idx ON boot_events (mac, occurred_at DESC);
