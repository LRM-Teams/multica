-- Canonical Message-backed Agent Creation action state (LRM-2343).
--
-- The agent:create proposal lives on a canonical channel_message (its parts
-- carry name/description/preferred Computer as an anchored reference part).
-- This table is the *commit state* for that Message: it records prepared vs
-- executed, the proposed snapshot, the final (non-sensitive) payload hash used
-- for idempotent replay, the human committer and the resulting Agent.
--
-- It replaces the transitional agent_action_card commit seam for the canonical
-- path. The Message id is the single identity; there are only two business
-- states (prepared / executed) and no dismissed/rejected/expired/TTL terminal
-- states (LRM-2343 Implementation Decisions).

CREATE TABLE agent_action (
    channel_message_id UUID PRIMARY KEY REFERENCES channel_message(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    action_type TEXT NOT NULL CHECK (action_type IN ('agent:create')),
    status TEXT NOT NULL DEFAULT 'prepared'
        CHECK (status IN ('prepared', 'executed')),
    -- Proposed snapshot as prepared by the agent: name/description/preferred
    -- Computer. Non-sensitive by construction (no runtime/model/reasoning/credential).
    proposed_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- SHA-256 (hex) of the canonical JSON of the final non-sensitive payload
    -- that was actually committed. Used for idempotent replay: same
    -- action_message_id + same hash -> return the same Agent; different -> 409.
    final_payload_hash TEXT,
    -- The human who clicked Create and committed (audit actor / owner).
    committer_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    -- The resulting Agent for an executed action.
    result_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    -- The proposing Agent (retained for provenance; not the creator/committer).
    prepared_by_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    prepared_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    executed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_action_workspace_created
    ON agent_action(workspace_id, created_at DESC);

CREATE INDEX idx_agent_action_result_agent
    ON agent_action(result_agent_id)
    WHERE result_agent_id IS NOT NULL;
