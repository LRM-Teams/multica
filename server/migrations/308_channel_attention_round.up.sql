-- Channel Attention Round: the durable record of one "should I participate?"
-- probe round for a group message that has no explicit agent @-mention. Each
-- available agent runs a cheap, tool-free attention probe; the round collects
-- per-agent decisions and resolves them (unique ANSWER grants a public
-- response; multiple ANSWERs go through one convergence round; COORDINATE or
-- unresolved conflict escalates to the group manager).
--
-- This is the persistence layer for the Attention Round feature (LRM-1528).
-- The execution + resolution logic lives in the daemon; this schema stores the
-- round state so dispatch can be auditable, resumable and grant-gated.

CREATE TABLE channel_attention_round (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
    trigger_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
    seq_from BIGINT NOT NULL CHECK (seq_from > 0),
    seq_to BIGINT NOT NULL CHECK (seq_to >= seq_from),
    status TEXT NOT NULL DEFAULT 'collecting'
        CHECK (status IN ('collecting', 'resolving', 'resolved', 'timed_out', 'failed')),
    expected_agent_count INTEGER NOT NULL DEFAULT 0 CHECK (expected_agent_count >= 0),
    completed_agent_count INTEGER NOT NULL DEFAULT 0
        CHECK (completed_agent_count >= 0 AND completed_agent_count <= expected_agent_count),
    dispatch_at TIMESTAMPTZ NOT NULL,
    deadline_at TIMESTAMPTZ NOT NULL CHECK (deadline_at > created_at),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (dispatch_at >= created_at),
    CHECK (dispatch_at < deadline_at)
);

-- Only one round may be collecting per channel at a time; the next message
-- either joins it or waits, matching the 2-5s debounce window.
CREATE UNIQUE INDEX idx_channel_attention_round_collecting_channel
    ON channel_attention_round(channel_id)
    WHERE status = 'collecting';

CREATE INDEX idx_channel_attention_round_deadline
    ON channel_attention_round(deadline_at, id)
    WHERE status IN ('collecting', 'resolving');

CREATE TABLE channel_attention_participant (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    round_id UUID NOT NULL REFERENCES channel_attention_round(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'decided', 'failed', 'timed_out', 'unavailable')),
    decision TEXT
        CHECK (decision IS NULL OR decision IN ('SILENT', 'ANSWER', 'CONTRIBUTE', 'COORDINATE')),
    confidence DOUBLE PRECISION
        CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    value_type TEXT
        CHECK (value_type IS NULL OR value_type IN ('none', 'direct_answer', 'unique_evidence', 'correction', 'task_claim', 'needs_protocol')),
    summary TEXT NOT NULL DEFAULT '',
    evidence_refs JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
    seen_up_to_seq BIGINT CHECK (seen_up_to_seq IS NULL OR seen_up_to_seq >= 0),
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    model_version TEXT,
    latency_ms BIGINT CHECK (latency_ms IS NULL OR latency_ms >= 0),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (round_id, agent_id)
);

CREATE INDEX idx_channel_attention_participant_round_status
    ON channel_attention_participant(round_id, status, agent_id);

-- At most one public-response grant per round: after resolution, exactly one
-- agent may publish to the channel (or the round escalates to the manager).
CREATE TABLE channel_attention_response_grant (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    round_id UUID NOT NULL REFERENCES channel_attention_round(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    grant_type TEXT NOT NULL
        CHECK (grant_type IN ('unique_answer', 'converged', 'manager_fallback')),
    status TEXT NOT NULL DEFAULT 'granted'
        CHECK (status IN ('granted', 'consumed', 'expired', 'revoked')),
    reason TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '10 minutes',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (round_id),
    CHECK (expires_at > created_at)
);

CREATE INDEX idx_channel_attention_response_grant_agent
    ON channel_attention_response_grant(agent_id, status, expires_at);
