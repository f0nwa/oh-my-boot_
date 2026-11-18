CREATE TABLE IF NOT EXISTS password_policy (
 id SMALLINT PRIMARY KEY CHECK (id = 1),
 max_age_days INT NOT NULL DEFAULT 0 CHECK (max_age_days BETWEEN 0 AND 3650),
 warn_days INT NOT NULL DEFAULT 7 CHECK (warn_days BETWEEN 0 AND 90),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO password_policy (id, max_age_days, warn_days) VALUES (1, 0, 7) ON CONFLICT (id) DO NOTHING;

ALTER TABLE users ADD COLUMN IF NOT EXISTS password_changed_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE users ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_policy_mode TEXT NOT NULL DEFAULT 'policy' CHECK (password_policy_mode IN ('policy', 'never', 'custom'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_max_age_days INT CHECK (password_max_age_days IS NULL OR password_max_age_days BETWEEN 1 AND 3650);
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_password_policy_custom_check;
ALTER TABLE users ADD CONSTRAINT users_password_policy_custom_check CHECK (
 (password_policy_mode = 'custom' AND password_max_age_days BETWEEN 1 AND 3650)
 OR (password_policy_mode <> 'custom' AND password_max_age_days IS NULL)
);
