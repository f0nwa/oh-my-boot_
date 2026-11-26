-- Split Windows boot.wim and install.wim/.esd management and independent swap support.

CREATE TABLE IF NOT EXISTS windows_boot_images (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('iso', 'file')),
    windows_profile_id BIGINT UNIQUE REFERENCES windows_profiles(id) ON DELETE CASCADE,
    storage_id BIGINT CHECK (storage_id IS NULL OR storage_id > 0),
    path TEXT NOT NULL DEFAULT '',
    storage_root TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    modified_at TIMESTAMPTZ,
    title TEXT NOT NULL CHECK (btrim(title) <> ''),
    sha256 TEXT NOT NULL DEFAULT '' CHECK (sha256 = '' OR sha256 ~ '^[0-9a-f]{64}$'),
    has_setup_exe BOOLEAN NOT NULL DEFAULT FALSE,
    wim_info JSONB NOT NULL DEFAULT '{}'::jsonb,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'preparing', 'ready', 'error', 'unavailable')),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (source_kind = 'iso' AND windows_profile_id IS NOT NULL AND storage_id IS NULL AND path = '')
        OR (source_kind = 'file' AND windows_profile_id IS NULL AND storage_id IS NOT NULL AND btrim(path) <> '')
    )
);
CREATE UNIQUE INDEX IF NOT EXISTS windows_boot_images_file_uniq
    ON windows_boot_images (storage_id, path) WHERE source_kind = 'file';

CREATE TABLE IF NOT EXISTS windows_install_images (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    storage_id BIGINT NOT NULL CHECK (storage_id > 0),
    path TEXT NOT NULL CHECK (btrim(path) <> ''),
    storage_root TEXT NOT NULL,
    install_file TEXT NOT NULL CHECK (install_file IN ('install.wim', 'install.esd')),
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    modified_at TIMESTAMPTZ NOT NULL,
    title TEXT NOT NULL CHECK (btrim(title) <> ''),
    wim_info JSONB NOT NULL DEFAULT '{}'::jsonb,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'preparing', 'ready', 'error', 'unavailable')),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (storage_id, path)
);

ALTER TABLE boot_profiles DROP CONSTRAINT IF EXISTS boot_profiles_kind_check;
ALTER TABLE boot_profiles ADD CONSTRAINT boot_profiles_kind_check
    CHECK (kind IN ('windows_iso', 'winpe_iso', 'ubuntu_builtin', 'ubuntu_iso', 'windows_wim'));

CREATE TABLE IF NOT EXISTS windows_install_boot_profiles (
    boot_profile_id BIGINT PRIMARY KEY REFERENCES boot_profiles(id) ON DELETE CASCADE,
    install_image_id BIGINT NOT NULL UNIQUE REFERENCES windows_install_images(id) ON DELETE RESTRICT
);

ALTER TABLE windows_installation_options
    ADD COLUMN IF NOT EXISTS boot_image_id BIGINT REFERENCES windows_boot_images(id) ON DELETE RESTRICT;
ALTER TABLE windows_installation_options
    ADD COLUMN IF NOT EXISTS install_image_id BIGINT REFERENCES windows_install_images(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS windows_installation_options_boot_image_idx
    ON windows_installation_options (boot_image_id) WHERE boot_image_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS windows_installation_options_install_image_idx
    ON windows_installation_options (install_image_id) WHERE install_image_id IS NOT NULL;

INSERT INTO windows_boot_images (source_kind, windows_profile_id, title, has_setup_exe, state)
SELECT 'iso', id, COALESCE(NULLIF(btrim(title), ''), 'Windows ISO #' || id::text), TRUE, 'ready'
FROM windows_profiles
WHERE boot_mode = '' AND state = 'ready'
ON CONFLICT (windows_profile_id) DO NOTHING;
