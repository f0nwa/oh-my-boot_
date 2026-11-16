-- Сохранение автора конфигураций WinPE и разовое заполнение существующих записей
-- (сначала по прямому событию driver-config.create, затем приближённо по загрузке).

ALTER TABLE winpe_driver_configs
 ADD COLUMN IF NOT EXISTS author TEXT NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS author_inferred BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE winpe_driver_configs c
SET author = direct.username,
    author_inferred = FALSE
FROM (
 SELECT DISTINCT ON (cfg_id)
  CAST(substring(a.detail FROM 'id=([0-9]+)') AS BIGINT) AS cfg_id,
  u.username
 FROM audit_events a
 JOIN users u ON u.id = a.actor_id
 WHERE a.result = 'ok'
   AND a.action = 'driver-config.create'
   AND a.detail ~ '(^| )id=[0-9]+'
   AND u.username <> ''
 ORDER BY cfg_id, a.id ASC
) direct
WHERE c.id = direct.cfg_id
  AND c.author = '';

UPDATE winpe_driver_configs c
SET author = inferred.username,
    author_inferred = TRUE
FROM (
 SELECT DISTINCT ON (wio.driver_config_id)
  wio.driver_config_id AS cfg_id,
  u.username
 FROM windows_installation_options wio
 JOIN winpe_driver_configs dc ON dc.id = wio.driver_config_id
 JOIN audit_events a ON a.result = 'ok'
   AND a.action = 'installation.create'
   AND a.detail = 'install_' || wio.installation_id::text
 JOIN users u ON u.id = a.actor_id
 WHERE wio.driver_config_id IS NOT NULL
   AND u.username <> ''
 ORDER BY wio.driver_config_id, ABS(EXTRACT(EPOCH FROM (a.occurred_at - dc.created_at))) ASC, a.id ASC
) inferred
WHERE c.id = inferred.cfg_id
  AND c.author = '';
