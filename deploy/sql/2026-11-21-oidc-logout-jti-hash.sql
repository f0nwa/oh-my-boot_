-- Reconcile the October (jti_hash) and November (jti) logout tables.
-- Stop Control before applying this migration and before restoring a backup.
DO $migration$
DECLARE
	identifier_column text;
BEGIN
	LOCK TABLE public.oidc_logout_jti IN ACCESS EXCLUSIVE MODE;

	-- Only accept the two known layouts with a single-column primary key.
	SELECT a.attname INTO identifier_column
	FROM pg_catalog.pg_attribute a
	JOIN pg_catalog.pg_constraint c ON c.conrelid = a.attrelid
		AND c.contype = 'p' AND c.conkey = ARRAY[a.attnum]
	WHERE a.attrelid = 'public.oidc_logout_jti'::regclass
		AND a.attname IN ('jti', 'jti_hash') AND a.atttypid = 'text'::regtype
		AND a.attnum > 0 AND NOT a.attisdropped;

	IF identifier_column IS NULL OR (
		SELECT count(*) FROM pg_catalog.pg_attribute
		WHERE attrelid = 'public.oidc_logout_jti'::regclass AND attnum > 0 AND NOT attisdropped
	) <> 2 OR NOT EXISTS (
		SELECT FROM pg_catalog.pg_attribute
		WHERE attrelid = 'public.oidc_logout_jti'::regclass
			AND attname = 'expires_at' AND atttypid = 'timestamptz'::regtype
			AND attnum > 0 AND NOT attisdropped AND attnotnull
	) THEN
		RAISE EXCEPTION 'unexpected oidc_logout_jti schema';
	END IF;

	IF identifier_column = 'jti' THEN
		-- Match Control's SHA-256 over UTF-8 with lowercase hexadecimal output.
		ALTER TABLE public.oidc_logout_jti
			ALTER COLUMN jti TYPE text USING encode(sha256(convert_to(jti, 'UTF8')), 'hex');
		ALTER TABLE public.oidc_logout_jti RENAME COLUMN jti TO jti_hash;
	END IF;
END;
$migration$;
