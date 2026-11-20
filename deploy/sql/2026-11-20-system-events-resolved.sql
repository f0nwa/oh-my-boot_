-- Migration: Add resolved_at and active index to system_events, backfill resolved events.
ALTER TABLE system_events ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS system_events_active_idx ON system_events (severity, occurred_at DESC) WHERE resolved_at IS NULL;

-- Backfill: mark historical error/warning rows as resolved if an info/recovery row exists
-- with the same source_key occurring at or after the incident.
WITH resolved_targets AS (
  SELECT err.id, MIN(rec.occurred_at) AS resolved_time
  FROM system_events err
  JOIN system_events rec
    ON err.source_key = rec.source_key
   AND err.source_key <> ''
   AND err.severity IN ('error', 'warning')
   AND rec.severity = 'info'
   AND rec.occurred_at >= err.occurred_at
  WHERE err.resolved_at IS NULL
  GROUP BY err.id
)
UPDATE system_events e
SET resolved_at = rt.resolved_time
FROM resolved_targets rt
WHERE e.id = rt.id;
