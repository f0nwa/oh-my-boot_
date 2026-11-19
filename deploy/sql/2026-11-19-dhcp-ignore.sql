-- DHCP ignore flag for machines (rendered as <mac>,ignore in /var/lib/omb-dhcp/hosts).
ALTER TABLE machines
    ADD COLUMN IF NOT EXISTS dhcp_ignored BOOLEAN NOT NULL DEFAULT FALSE;
