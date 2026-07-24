-- LRM-671 D1: one canonical provider session/workspace pointer per
-- (agent, runtime). This table is runtime state only. It deliberately has no
-- dependency on agent_task_queue because that queue is a transitional wake
-- source and will be removed in a later cutover phase.
CREATE TABLE agent_runtime_state (
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    runtime_id UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE CASCADE,
    provider_session_id TEXT,
    work_dir TEXT,
    provider_config_fingerprint TEXT,
    generation BIGINT NOT NULL DEFAULT 1,
    last_turn_id UUID,
    fresh_session_notice_reason TEXT,
    legacy_resume_archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, runtime_id),
    CHECK (provider_session_id IS NULL OR btrim(provider_session_id) <> ''),
    CHECK (work_dir IS NULL OR btrim(work_dir) <> ''),
    CHECK (provider_config_fingerprint IS NULL OR btrim(provider_config_fingerprint) <> ''),
    CHECK (generation >= 1),
    CONSTRAINT agent_runtime_state_notice_reason_check CHECK (
        fresh_session_notice_reason IS NULL
        OR fresh_session_notice_reason IN ('cutover', 'reset')
    ),
    -- A persisted provider session consumes the one-time notice atomically.
    CONSTRAINT agent_runtime_state_notice_session_check CHECK (
        provider_session_id IS NULL
        OR fresh_session_notice_reason IS NULL
    ),
    -- Once legacy resume state was archived, an empty canonical session must
    -- keep an explicit cutover/reset notice instead of becoming a silent gap.
    CONSTRAINT agent_runtime_state_archived_empty_notice_check CHECK (
        legacy_resume_archived_at IS NULL
        OR provider_session_id IS NOT NULL
        OR fresh_session_notice_reason IS NOT NULL
    )
);

-- Migration A is intentionally honest: fragmented legacy chat/issue provider
-- sessions cannot be merged. Seed one empty canonical row for every existing
-- agent on its current runtime and mark it for the future first-wake notice.
-- This migration does not archive, read-switch, or mutate legacy task/chat
-- session rows or directories: they remain the only live resume source until
-- D6 drains execution and records the real archive boundary.
INSERT INTO agent_runtime_state (
    agent_id,
    runtime_id,
    provider_session_id,
    work_dir,
    provider_config_fingerprint,
    generation,
    last_turn_id,
    fresh_session_notice_reason,
    legacy_resume_archived_at
)
SELECT
    a.id,
    a.runtime_id,
    NULL,
    NULL,
    NULL,
    1,
    NULL,
    'cutover',
    NULL
FROM agent a
ON CONFLICT (agent_id, runtime_id) DO NOTHING;
