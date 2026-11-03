BEGIN;

-- Keep historical identities, positions and component snapshots after an
-- explicit boot entry deletion. Control validates new publications and guards
-- active entries in its deletion transaction. No history or library is erased.
ALTER TABLE menu_version_items DROP CONSTRAINT IF EXISTS menu_version_items_installation_id_fkey;

COMMIT;
