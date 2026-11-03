BEGIN;

ALTER TABLE menu_draft_items ADD COLUMN IF NOT EXISTS components JSONB;
ALTER TABLE menu_version_items ADD COLUMN IF NOT EXISTS components JSONB;

-- Before editable boot entries, component bindings were immutable. Capture
-- those bindings once, preserving the existing menu IDs, titles and order.
UPDATE menu_draft_items m SET components=jsonb_build_object(
 'title',i.title,'folder',i.folder,
 'answer_id',COALESCE(w.answer_id,0),
 'driver_config_id',COALESCE(w.driver_config_id,0),
 'autoinstall_id',COALESCE(i.autoinstall_id,0))
FROM installations i LEFT JOIN windows_installation_options w ON w.installation_id=i.id
WHERE m.installation_id=i.id AND m.components IS NULL;

UPDATE menu_version_items m SET components=jsonb_build_object(
 'title',i.title,'folder',i.folder,
 'answer_id',COALESCE(w.answer_id,0),
 'driver_config_id',COALESCE(w.driver_config_id,0),
 'autoinstall_id',COALESCE(i.autoinstall_id,0))
FROM installations i LEFT JOIN windows_installation_options w ON w.installation_id=i.id
WHERE m.installation_id=i.id AND m.components IS NULL;

DROP TRIGGER IF EXISTS installation_autoinstall_immutable ON installations;
DROP FUNCTION IF EXISTS reject_autoinstall_binding_update();

COMMIT;
