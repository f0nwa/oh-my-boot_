-- Unified system events and incidents log for OMB control dashboard.
CREATE TABLE IF NOT EXISTS system_events (
 id BIGSERIAL PRIMARY KEY,
 occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 severity TEXT NOT NULL CHECK (severity IN ('error', 'warning', 'info')),
 category TEXT NOT NULL CHECK (category IN ('storage', 'service', 'network', 'task')),
 source_key TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL,
 detail TEXT NOT NULL DEFAULT '',
 link TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS system_events_occurred_idx ON system_events (occurred_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS system_events_severity_idx ON system_events (severity, occurred_at DESC);

DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'omb-control') THEN
  ALTER TABLE system_events OWNER TO "omb-control";
  ALTER SEQUENCE system_events_id_seq OWNER TO "omb-control";
 END IF;
END $$;
