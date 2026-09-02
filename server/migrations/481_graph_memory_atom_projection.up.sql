-- Task 7: stable Memory Atom projection surface (spec 8.1/8.2).
--
-- graph_memory_atom is the durable Atom ledger: one row per stable,
-- segment-scoped atom identity, written exclusively inside the publish
-- transaction together with the segment's publish_seq and the sanitized
-- payload. Identity fields are immutable; active/retracted state is derived
-- downstream (retraction fence, migration 467) and never mutates these rows.
--
-- graph_memory_projection_outbox is the durable Graph projection request the
-- publish transaction emits. Task 8's projector may only claim these rows; it
-- must not infer work by scanning files or atoms.

-- Composite uniqueness for workspace-scoped foreign keys. Legacy-unverified
-- rows carry a NULL workspace and stay outside the key.
CREATE UNIQUE INDEX IF NOT EXISTS interaction_dag_segment_workspace_segment_466_key
    ON interaction_dag_segment (workspace_id, segment_id)
    WHERE workspace_id IS NOT NULL;

CREATE TABLE graph_memory_atom (
    workspace_id uuid NOT NULL,
    atom_id text NOT NULL,
    segment_id text NOT NULL,
    body text NOT NULL,
    kind text NOT NULL,
    source_message_seqs integer[] NOT NULL DEFAULT '{}',
    source_tool text NOT NULL DEFAULT '',
    tool_trust_class text NOT NULL,
    content_hash text NOT NULL,
    artifact_ref text,
    visibility text NOT NULL,
    channel_id uuid,
    project_id uuid,
    publish_seq bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, atom_id),
    CONSTRAINT graph_memory_atom_segment_fk
        FOREIGN KEY (workspace_id, segment_id)
        REFERENCES interaction_dag_segment (workspace_id, segment_id),
    CONSTRAINT graph_memory_atom_kind_check CHECK (kind IN ('fact', 'preference', 'fallback')),
    CONSTRAINT graph_memory_atom_trust_check
        CHECK (tool_trust_class IN ('none', 'trusted_read_only', 'mutation', 'unknown')),
    CONSTRAINT graph_memory_atom_visibility_check CHECK (visibility IN ('channel', 'project')),
    CONSTRAINT graph_memory_atom_scope_check CHECK (
        (visibility = 'channel' AND channel_id IS NOT NULL AND project_id IS NULL)
        OR (visibility = 'project' AND project_id IS NOT NULL AND channel_id IS NULL)
    ),
    CONSTRAINT graph_memory_atom_body_budget_check CHECK (
        length(btrim(body)) > 0
        AND cardinality(source_message_seqs) <= 32
    ),
    CONSTRAINT graph_memory_atom_publish_check CHECK (publish_seq > 0),
    CONSTRAINT graph_memory_atom_fallback_shape_check CHECK (
        (kind <> 'fallback')
        OR (source_message_seqs = '{}' AND source_tool = '' AND tool_trust_class = 'none')
    )
);

CREATE INDEX graph_memory_atom_segment_idx ON graph_memory_atom (workspace_id, segment_id);
-- Scope-driven reads: channel atoms resolve per channel, project atoms per
-- project, so the default search corpus never crosses a scope boundary.
CREATE INDEX graph_memory_atom_scope_idx
    ON graph_memory_atom (workspace_id, visibility, channel_id, project_id, publish_seq);

-- Atom rows are write-once. Retraction is ledger-derived (never a row
-- mutation) and retention erasure arrives with the 467 fence as an explicit
-- authorized procedure.
CREATE OR REPLACE FUNCTION protect_graph_memory_atom()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'graph memory atom rows are write-once: % is forbidden', TG_OP;
END;
$$;

CREATE TRIGGER graph_memory_atom_write_once
    BEFORE UPDATE OR DELETE ON graph_memory_atom
    FOR EACH ROW EXECUTE FUNCTION protect_graph_memory_atom();

-- Every referenced sequence must sit inside the owning segment's canonical
-- task_messages range, and the segment must be published: atoms never exist
-- for content that is not yet readable.
CREATE OR REPLACE FUNCTION validate_graph_memory_atom()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  segment_status text;
  segment_start integer;
  segment_end integer;
  out_of_range boolean;
BEGIN
  SELECT publish_status, start_seq, end_seq
    INTO segment_status, segment_start, segment_end
  FROM interaction_dag_segment
  WHERE workspace_id = NEW.workspace_id
    AND segment_id = NEW.segment_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'graph memory atom references an unknown segment';
  END IF;
  IF segment_status <> 'published' THEN
    RAISE EXCEPTION 'graph memory atoms may only exist for published segments';
  END IF;
  SELECT EXISTS (
    SELECT 1
    FROM unnest(NEW.source_message_seqs) AS seq
    WHERE seq < segment_start OR seq > segment_end
  ) INTO out_of_range;
  IF out_of_range THEN
    RAISE EXCEPTION 'graph memory atom cites a message outside the segment range';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER graph_memory_atom_validate
    BEFORE INSERT ON graph_memory_atom
    FOR EACH ROW EXECUTE FUNCTION validate_graph_memory_atom();

CREATE TABLE graph_memory_projection_outbox (
    workspace_id uuid NOT NULL,
    segment_id text NOT NULL,
    request_hash text NOT NULL,
    route_generation bigint,
    status text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz,
    lease_owner text,
    lease_expires_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (workspace_id, segment_id),
    CONSTRAINT graph_memory_projection_segment_fk
        FOREIGN KEY (workspace_id, segment_id)
        REFERENCES interaction_dag_segment (workspace_id, segment_id)
        ON DELETE CASCADE,
    CONSTRAINT graph_memory_projection_status_check
        CHECK (status IN ('pending', 'processing', 'retry', 'completed', 'dead_letter')),
    CONSTRAINT graph_memory_projection_attempts_check CHECK (attempts >= 0),
    CONSTRAINT graph_memory_projection_lease_shape_check
        CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
    CONSTRAINT graph_memory_projection_lease_states_check
        CHECK (status IN ('processing') OR lease_owner IS NULL),
    CONSTRAINT graph_memory_projection_retry_metadata_check
        CHECK (status <> 'retry' OR (next_attempt_at IS NOT NULL AND lease_owner IS NULL)),
    CONSTRAINT graph_memory_projection_clear_metadata_check
        CHECK (status NOT IN ('pending', 'completed', 'dead_letter')
               OR (next_attempt_at IS NULL AND lease_owner IS NULL))
);

CREATE INDEX graph_memory_projection_claimable_idx
    ON graph_memory_projection_outbox (status, next_attempt_at, updated_at)
    WHERE status IN ('pending', 'retry');
