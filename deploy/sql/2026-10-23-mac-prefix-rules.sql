-- MAC prefix rules: a name (and optionally the network card's maker) for
-- every machine whose MAC starts with the first 3-5 bytes.
CREATE TABLE IF NOT EXISTS mac_prefix_rules (
    prefix TEXT PRIMARY KEY CHECK (prefix ~ '^[0-9a-f]{2}(:[0-9a-f]{2}){2,4}$'),
    title TEXT NOT NULL CHECK (title <> ''),
    manufacturer TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
