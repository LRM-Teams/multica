-- 499: Graph Memory evaluation protocol plane (test-only).
--
-- Independent from skill_evaluation_run / research evaluation objects on
-- purpose: a PAST-style episode needs mutable lifecycle state (pending ->
-- running -> settled) plus an immutable arm/policy/session binding, and a
-- usage ledger that may never be rewritten. Three tables:
--
--   graph_memory_evaluation_run        one protocol pass (batch header)
--   graph_memory_evaluation_episode    one (run, episode) with arm policy,
--                                      channel/agent binding, input/output
--                                      message ids, closure state
--   graph_memory_evaluation_usage_event append-only usage/evidence ledger
--
-- Fail-closed rules held by the database, not just the store:
--   * episode arm/memory_policy/session/channel/agent bindings are immutable
--     after insert (trigger) — an arm cannot drift mid-episode;
--   * at most one live (pending|running) episode per channel (partial unique
--     index) — the policy lookup used by recall/gateway/capture enforcement
--     is unambiguous by construction;
--   * usage_event is append-only (trigger);
--   * official scoring can only land on a settled episode and can never turn
--     'unsupported' into a score (CHECK).
--
-- The whole plane is additionally gated server-side by a test-only
-- configuration gate plus explicit workspace allowlist; this migration only
-- creates the schema, it enables nothing.

CREATE TABLE graph_memory_evaluation_run (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL CHECK (run_id <> '' AND length(run_id) <= 256),
    label TEXT NOT NULL DEFAULT '' CHECK (length(label) <= 256),
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed', 'aborted')),
    created_by_actor TEXT NOT NULL CHECK (created_by_actor <> '' AND length(created_by_actor) <= 256),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (workspace_id, run_id)
);

CREATE INDEX idx_graph_memory_evaluation_run_created
    ON graph_memory_evaluation_run (workspace_id, created_at DESC);

CREATE TABLE graph_memory_evaluation_episode (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL,
    episode_id TEXT NOT NULL CHECK (episode_id <> '' AND length(episode_id) <= 256),
    channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
    primary_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    -- Arm policy is authoritative here; the same value is mirrored into
    -- memory_policy for harness bookkeeping joins without a policy debate.
    arm TEXT NOT NULL CHECK (arm IN ('graph_on', 'persistence_off')),
    memory_policy TEXT NOT NULL CHECK (memory_policy IN ('graph_on', 'persistence_off')),
    -- Attested provider session generation token; part of the closure
    -- contract (fresh-session attestation) and immutable after insert.
    session_generation TEXT NOT NULL CHECK (session_generation <> '' AND length(session_generation) <= 128),
    input_message_id UUID,
    output_message_id UUID,
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'settled', 'failed', 'skipped')),
    -- Per-condition closure evidence: keys are the seven closure conditions,
    -- values are {state: complete|partial|unknown, detail: text}. Unknown is
    -- never coerced to complete; settle() refuses while any required
    -- condition is not complete.
    closure_checklist JSONB NOT NULL DEFAULT '{}'::jsonb,
    official_score_state TEXT NOT NULL DEFAULT 'unscored'
        CHECK (official_score_state IN ('unscored', 'unsupported', 'ready', 'scored')),
    official_score JSONB,
    failure_reason TEXT NOT NULL DEFAULT '' CHECK (length(failure_reason) <= 1024),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    settled_at TIMESTAMPTZ,
    PRIMARY KEY (workspace_id, run_id, episode_id),
    CONSTRAINT graph_memory_evaluation_episode_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES graph_memory_evaluation_run (workspace_id, run_id)
        ON DELETE CASCADE
);

CREATE INDEX idx_graph_memory_evaluation_episode_channel
    ON graph_memory_evaluation_episode (workspace_id, channel_id, created_at DESC);

-- Exactly one live episode per channel: the enforcement lookup
-- "active episode arm for this channel" cannot be ambiguous.
CREATE UNIQUE INDEX uq_graph_memory_evaluation_episode_live_channel
    ON graph_memory_evaluation_episode (workspace_id, channel_id)
    WHERE status IN ('pending', 'running');

CREATE INDEX idx_graph_memory_evaluation_episode_run
    ON graph_memory_evaluation_episode (workspace_id, run_id, created_at);

-- Bindings are immutable after insert: arm/policy flip mid-episode or
-- rebinding to another channel/agent would silently invalidate the ablation.
CREATE FUNCTION graph_memory_evaluation_binding_immutable() RETURNS trigger AS $$
BEGIN
    IF NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
       OR NEW.run_id IS DISTINCT FROM OLD.run_id
       OR NEW.episode_id IS DISTINCT FROM OLD.episode_id
       OR NEW.channel_id IS DISTINCT FROM OLD.channel_id
       OR NEW.primary_agent_id IS DISTINCT FROM OLD.primary_agent_id
       OR NEW.arm IS DISTINCT FROM OLD.arm
       OR NEW.memory_policy IS DISTINCT FROM OLD.memory_policy
       OR NEW.session_generation IS DISTINCT FROM OLD.session_generation THEN
        RAISE EXCEPTION 'graph_memory_evaluation_episode bindings are immutable after insert'
            USING ERRCODE = 'raise_exception';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER graph_memory_evaluation_episode_binding_immutable
    BEFORE UPDATE ON graph_memory_evaluation_episode
    FOR EACH ROW EXECUTE FUNCTION graph_memory_evaluation_binding_immutable();

-- Official scoring integrity: only settled episodes may hold a score, and an
-- explicitly unsupported episode can never become scored (missing evidence
-- stays unscored forever — the store cannot invent a score).
ALTER TABLE graph_memory_evaluation_episode
    ADD CONSTRAINT graph_memory_evaluation_score_settled_check
    CHECK (
        (official_score_state IN ('unscored', 'unsupported')
         AND official_score IS NULL)
        OR (official_score_state IN ('ready', 'scored') AND settled_at IS NOT NULL)
    );
ALTER TABLE graph_memory_evaluation_episode
    ADD CONSTRAINT graph_memory_evaluation_unsupported_never_scored_check
    CHECK (NOT (official_score_state = 'unsupported' AND official_score IS NOT NULL));
ALTER TABLE graph_memory_evaluation_episode
    ADD CONSTRAINT graph_memory_evaluation_scored_payload_check
    CHECK (NOT (official_score_state = 'scored' AND official_score IS NULL));

CREATE TABLE graph_memory_evaluation_usage_event (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    event_id UUID NOT NULL DEFAULT gen_random_uuid(),
    run_id TEXT NOT NULL,
    episode_id TEXT NOT NULL,
    session_generation TEXT NOT NULL DEFAULT '' CHECK (length(session_generation) <= 128),
    kind TEXT NOT NULL CHECK (kind IN (
        'provider_tokens', 'memory_agent_tokens', 'mcp_call',
        'gateway_operation', 'recall_request', 'recall_injection',
        'auto_checkpoint', 'atom_published', 'session_reset',
        'notes_tool_dispatch', 'artifact_snapshot', 'policy_denial'
    )),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, event_id),
    CONSTRAINT graph_memory_evaluation_usage_episode_fk
        FOREIGN KEY (workspace_id, run_id, episode_id)
        REFERENCES graph_memory_evaluation_episode (workspace_id, run_id, episode_id)
        ON DELETE CASCADE
);

CREATE INDEX idx_graph_memory_evaluation_usage_episode
    ON graph_memory_evaluation_usage_event (workspace_id, run_id, episode_id, created_at);

CREATE FUNCTION graph_memory_evaluation_usage_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION '% is append-only (graph memory evaluation usage): rows are immutable', TG_TABLE_NAME
        USING ERRCODE = 'raise_exception';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER graph_memory_evaluation_usage_append_only
    BEFORE UPDATE OR DELETE ON graph_memory_evaluation_usage_event
    FOR EACH ROW EXECUTE FUNCTION graph_memory_evaluation_usage_append_only();
