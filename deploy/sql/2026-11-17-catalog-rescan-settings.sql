-- Настройки периодического автоматического сканирования каталогов ISO в omb_images.
CREATE TABLE IF NOT EXISTS catalog_settings (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    rescan_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    rescan_interval_minutes INT NOT NULL DEFAULT 30 CHECK (rescan_interval_minutes BETWEEN 1 AND 1440),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO catalog_settings (id, rescan_enabled, rescan_interval_minutes)
VALUES (1, TRUE, 30)
ON CONFLICT (id) DO NOTHING;

DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'omb-images') THEN
        ALTER TABLE catalog_settings OWNER TO "omb-images";
    END IF;
END $$;
