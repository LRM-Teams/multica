-- Period Work Brief orchestration: durable collector status + retry counts
-- so synthesis waits on status (not a fixed clock) and the synthesizer can
-- narrowly re-dispatch retryable collectors (max 3 retries).

CREATE TABLE IF NOT EXISTS note_period_brief_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    owner_user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    draft_page_id UUID NOT NULL UNIQUE REFERENCES note_page(id) ON DELETE CASCADE,
    folder_page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    synthesizer_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    window_label TEXT NOT NULL DEFAULT '',
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'UTC',
    window_kind TEXT NOT NULL DEFAULT 'week',
    channel_id UUID,
    facts_text TEXT NOT NULL DEFAULT '',
    sources_used TEXT[] NOT NULL DEFAULT '{}',
    sources_empty TEXT[] NOT NULL DEFAULT '{}',
    sources_skipped TEXT[] NOT NULL DEFAULT '{}',
    -- [{agent_id, pack_page_id, job_id, channel_id, retry_count, window_label, window_start, window_end}]
    collectors JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'collecting',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT note_period_brief_run_status_check
        CHECK (status IN ('collecting', 'synthesizing', 'done'))
);

CREATE INDEX IF NOT EXISTS note_period_brief_run_workspace_owner_idx
    ON note_period_brief_run (workspace_id, owner_user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS note_period_brief_run_synth_idx
    ON note_period_brief_run (synthesizer_agent_id, created_at DESC);
