BEGIN;

-- Apply note: this migration is additive. Deploy it before a server that
-- writes Machine Upgrade rows; pre-capability servers ignore the new table.
-- Machine Upgrade is owned by a daemon, not by one of its runtime/provider
-- projections.  The daemon ID is the persistent machine identity advertised
-- by every sibling runtime, so the partial unique index is the database
-- boundary that prevents two active generations from being requested at once.
CREATE TABLE machine_upgrade (
    id TEXT PRIMARY KEY,
    daemon_id TEXT NOT NULL CHECK (btrim(daemon_id) <> ''),
    requested_by UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    request_id TEXT NOT NULL UNIQUE CHECK (btrim(request_id) <> ''),
    requested_target TEXT NOT NULL CHECK (btrim(requested_target) <> ''),
    resolved_target TEXT,
    phase TEXT NOT NULL DEFAULT 'queued'
        CHECK (phase IN (
            'queued', 'starting', 'staging', 'verifying', 'handoff',
            'converging', 'completed', 'failed', 'rolled_back', 'timeout',
            'cancelled'
        )),
    result TEXT CHECK (result IN ('completed', 'failed', 'rolled_back', 'timeout', 'cancelled')),
    error_code TEXT,
    error_message TEXT,
    accepted_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (phase IN ('completed', 'failed', 'rolled_back', 'timeout', 'cancelled'))
        = (result IS NOT NULL)
    )
);

CREATE UNIQUE INDEX machine_upgrade_one_active_per_daemon_idx
    ON machine_upgrade (daemon_id)
    WHERE phase NOT IN ('completed', 'failed', 'rolled_back', 'timeout', 'cancelled');

CREATE INDEX machine_upgrade_latest_by_daemon_idx
    ON machine_upgrade (daemon_id, updated_at DESC, created_at DESC, id DESC);

COMMIT;
