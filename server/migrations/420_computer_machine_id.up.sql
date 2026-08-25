-- Persist the OS-level persistent machine fingerprint on the Computer identity.
-- machine_id (e.g. /etc/machine-id, IOPlatformUUID, MachineGuid) is a property of
-- the physical machine, independent of ~/.multica, and is the authoritative
-- same-machine proof for identity reclaim and agent convergence (LRM-1570).
ALTER TABLE computer_identity_owner
    ADD COLUMN IF NOT EXISTS machine_id TEXT;
