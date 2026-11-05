BEGIN;
CREATE TABLE IF NOT EXISTS image_names (
 storage_id BIGINT NOT NULL CHECK (storage_id > 0),
 path TEXT NOT NULL,
 name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
 PRIMARY KEY (storage_id, path)
);
COMMIT;
