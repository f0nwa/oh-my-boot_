-- Every machine seen on the PXE network: what it reported about itself and
-- what people named it. Kept when the boot request log is cleared.
CREATE TABLE IF NOT EXISTS machines (
    mac TEXT PRIMARY KEY CHECK (mac ~ '^[0-9a-f]{2}(:[0-9a-f]{2}){5}$'),
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen TIMESTAMPTZ NOT NULL,
    last_ip TEXT NOT NULL DEFAULT '',
    firmware TEXT NOT NULL DEFAULT '',
    manufacturer TEXT NOT NULL DEFAULT '',
    product TEXT NOT NULL DEFAULT '',
    serial TEXT NOT NULL DEFAULT '',
    uuid TEXT NOT NULL DEFAULT '',
    detected_at TIMESTAMPTZ,
    name TEXT NOT NULL DEFAULT '',
    manufacturer_override TEXT NOT NULL DEFAULT '',
    product_override TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS machines_last_seen_idx ON machines (last_seen DESC);
