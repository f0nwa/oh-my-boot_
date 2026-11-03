-- Allow installer-selected disk erasure without changing existing answer files.
BEGIN;

ALTER TABLE ubuntu_autoinstall_files DROP CONSTRAINT IF EXISTS ubuntu_autoinstall_files_disk_mode_check;
ALTER TABLE ubuntu_autoinstall_files DROP CONSTRAINT IF EXISTS ubuntu_autoinstall_files_check;
ALTER TABLE ubuntu_autoinstall_files DROP CONSTRAINT IF EXISTS ubuntu_autoinstall_files_disk_serial_check;
ALTER TABLE ubuntu_autoinstall_files ADD CONSTRAINT ubuntu_autoinstall_files_disk_mode_check CHECK (disk_mode IN ('interactive','default','serial'));
ALTER TABLE ubuntu_autoinstall_files ADD CONSTRAINT ubuntu_autoinstall_files_disk_serial_check CHECK ((disk_mode IN ('interactive','default') AND disk_serial = '') OR (disk_mode = 'serial' AND octet_length(disk_serial) BETWEEN 1 AND 128));

COMMIT;
