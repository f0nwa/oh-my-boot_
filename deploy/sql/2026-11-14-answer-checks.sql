CREATE TABLE IF NOT EXISTS answer_installation_checks (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 windows_answer_id BIGINT REFERENCES windows_answer_variants(id) ON DELETE CASCADE,
 ubuntu_autoinstall_id BIGINT REFERENCES ubuntu_autoinstall_files(id) ON DELETE CASCADE,
 sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
 result TEXT NOT NULL CHECK (result IN ('success','failure')),
 machine TEXT NOT NULL DEFAULT '' CHECK (char_length(machine) <= 200),
 comment TEXT NOT NULL DEFAULT '' CHECK (char_length(comment) <= 2000),
 user_id BIGINT NOT NULL REFERENCES users(id),
 author TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (num_nonnulls(windows_answer_id,ubuntu_autoinstall_id) = 1)
);
CREATE INDEX IF NOT EXISTS answer_checks_windows_idx ON answer_installation_checks (windows_answer_id,id DESC) WHERE windows_answer_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS answer_checks_ubuntu_idx ON answer_installation_checks (ubuntu_autoinstall_id,id DESC) WHERE ubuntu_autoinstall_id IS NOT NULL;
