-- Task #81: MULTICA_PINNED_VERSION has only ever been daemon-local config
-- (internal/daemon/config.go, read once at startup from the env var) — the
-- server has never known a machine was pinned, so pin status could not be
-- shown on GET /agents/GET /api/computers and a server-initiated
-- InitiateUpdate could not check it. Per-runtime (not per-agent): the pin
-- applies to the whole daemon process/machine, not to an individual agent.
-- NULL = not pinned. Refreshed unconditionally on every DaemonRegister so
-- unpinning (env var removed) clears the stale value the same way
-- offline_reason/starting_since already do.
ALTER TABLE agent_runtime ADD COLUMN pinned_version TEXT;
