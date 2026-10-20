-- Admin UI redesign: answer-file disk flag and the boot menu draft.
ALTER TABLE windows_answer_variants ADD COLUMN IF NOT EXISTS wipes_disk BOOLEAN;
CREATE TABLE IF NOT EXISTS menu_draft (id SMALLINT PRIMARY KEY CHECK (id = 1), title TEXT NOT NULL, label TEXT NOT NULL, message TEXT NOT NULL, ubuntu BOOLEAN NOT NULL, ubuntu_folder TEXT NOT NULL DEFAULT '', updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS menu_draft_items (position INT PRIMARY KEY, key TEXT NOT NULL UNIQUE CHECK (key ~ '^win(iso|answer|driver)_[0-9]+$'), folder TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '');
