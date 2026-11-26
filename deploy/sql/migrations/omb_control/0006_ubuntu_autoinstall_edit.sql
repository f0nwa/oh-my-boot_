-- Allow in-place editing of constructor-generated Ubuntu Autoinstall scripts while keeping uploaded YAML files immutable (except title rename).
CREATE OR REPLACE FUNCTION reject_ubuntu_autoinstall_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id IS DISTINCT FROM OLD.id
    OR NEW.source_kind IS DISTINCT FROM OLD.source_kind
    OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
  RAISE EXCEPTION 'Ubuntu Autoinstall identity is immutable';
 END IF;
 IF OLD.source_kind = 'uploaded' AND (
    NEW."values" IS DISTINCT FROM OLD."values"
    OR NEW.user_data IS DISTINCT FROM OLD.user_data
    OR NEW.sha256 IS DISTINCT FROM OLD.sha256
    OR NEW.state IS DISTINCT FROM OLD.state
    OR NEW.disk_mode IS DISTINCT FROM OLD.disk_mode
    OR NEW.disk_serial IS DISTINCT FROM OLD.disk_serial
 ) THEN
  RAISE EXCEPTION 'Uploaded Ubuntu Autoinstall files are immutable';
 END IF;
 RETURN NEW;
END;
$$;
