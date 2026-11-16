-- Отвязка Windows autounattend.xml от конкретного ISO-профиля,
-- сохранение метаданных install.wim/esd и проверок по парам (скрипт, образ).

ALTER TABLE windows_profiles
 ADD COLUMN IF NOT EXISTS wim_info JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE windows_answer_variants
 ADD COLUMN IF NOT EXISTS target_summary JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE answer_installation_checks
 ADD COLUMN IF NOT EXISTS windows_profile_id BIGINT REFERENCES windows_profiles(id) ON DELETE SET NULL,
 ADD COLUMN IF NOT EXISTS profile_title TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
 IF EXISTS (
  SELECT 1
  FROM information_schema.columns
  WHERE table_schema = current_schema()
    AND table_name = 'windows_answer_variants'
    AND column_name = 'profile_id'
 ) THEN
  UPDATE answer_installation_checks c
  SET windows_profile_id = a.profile_id,
      profile_title = COALESCE(p.title, '')
  FROM windows_answer_variants a
  LEFT JOIN windows_profiles p ON p.id = a.profile_id
  WHERE c.windows_answer_id = a.id
    AND c.windows_profile_id IS NULL;
 END IF;
END $$;

UPDATE answer_installation_checks
SET profile_title = 'Ubuntu Desktop'
WHERE ubuntu_autoinstall_id IS NOT NULL
  AND profile_title = '';

DROP INDEX IF EXISTS windows_answer_variants_profile_idx;
ALTER TABLE windows_answer_variants DROP COLUMN IF EXISTS profile_id;

CREATE INDEX IF NOT EXISTS answer_checks_profile_idx
 ON answer_installation_checks (COALESCE(windows_answer_id,0), COALESCE(ubuntu_autoinstall_id,0), COALESCE(windows_profile_id,0), profile_title, id DESC);
