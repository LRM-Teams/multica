BEGIN;

-- Splits "the admin wants this runtime on the latest version" (a durable
-- intent) from "here is one specific delivery attempt" (daemon_runtime_update,
-- migration 217, correctly short-lived with a 120s pending timeout).
--
-- Incident (2026-08-01/02): InitiateUpdate created only an attempt row with a
-- 120-second pending window. A laptop asleep at the moment of the call simply
-- missed it — no queueing, no "deliver next time it's online". This table
-- gives the intent somewhere durable to live until the runtime is actually
-- reachable (a heartbeat arriving), instead of expiring in 120s regardless of
-- whether anyone was ever watching.
CREATE TABLE daemon_runtime_update_intent (
    runtime_id UUID PRIMARY KEY REFERENCES agent_runtime(id) ON DELETE CASCADE,
    -- Deliberately no target_version column: the intent always resolves to
    -- whatever RuntimeReleaseSource reports as latest at materialization
    -- time, not whatever was newest when the intent was created. An intent
    -- that outlives its target by days must not install a version we may
    -- have since found and fixed a bug in (Parker's call, 2026-08-02). A
    -- future pinned-version intent is not modeled here.
    created_by UUID NOT NULL REFERENCES member(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    cancelled_at TIMESTAMPTZ,
    -- Terminal marker for "aged out without ever being delivered". Never
    -- deleted on expiry — an expired intent stays as a visible row until an
    -- admin re-requests (a fresh InitiateUpdate replaces the row) or
    -- explicitly cancels it. Parker's explicit "no silent disappearance"
    -- rule, same session as the reminder-subsystem silent-drop fixes.
    expired_at TIMESTAMPTZ
);

CREATE INDEX daemon_runtime_update_intent_live_idx
    ON daemon_runtime_update_intent (runtime_id)
    WHERE cancelled_at IS NULL AND expired_at IS NULL;

COMMIT;
