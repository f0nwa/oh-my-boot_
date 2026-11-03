BEGIN;
ALTER TABLE machines ADD COLUMN IF NOT EXISTS network_driver TEXT NOT NULL DEFAULT ''
    CONSTRAINT machines_network_driver_check CHECK (network_driver IN ('', 'ipxe', 'snp'));
ALTER TABLE mac_prefix_rules ADD COLUMN IF NOT EXISTS network_driver TEXT NOT NULL DEFAULT ''
    CONSTRAINT mac_prefix_rules_network_driver_check CHECK (network_driver IN ('', 'ipxe', 'snp'));
COMMIT;
