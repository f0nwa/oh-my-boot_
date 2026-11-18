ALTER TABLE answer_installation_checks ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE answer_installation_checks DROP CONSTRAINT IF EXISTS answer_installation_checks_user_id_fkey;
ALTER TABLE answer_installation_checks ADD CONSTRAINT answer_installation_checks_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;
