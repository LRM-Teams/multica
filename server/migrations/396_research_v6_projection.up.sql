CREATE TABLE research_projection_snapshot (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 through_event_sequence BIGINT NOT NULL CHECK(through_event_sequence>=0), generation BIGINT NOT NULL CHECK(generation>=0),
 expires_at TIMESTAMPTZ NOT NULL, projection_hash TEXT NOT NULL CHECK(projection_hash ~ '^sha256:[0-9a-f]{64}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id), UNIQUE(session_id,generation,through_event_sequence),
 FOREIGN KEY(workspace_id,session_id) REFERENCES research_session(workspace_id,id) ON DELETE CASCADE
);
CREATE INDEX research_v6_projection_snapshot_latest_idx ON research_projection_snapshot(session_id,generation,through_event_sequence DESC);
CREATE TABLE research_projection_slice (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 snapshot_id UUID NOT NULL, slice_key TEXT NOT NULL, cursor_key TEXT NOT NULL DEFAULT '',
 node_count INTEGER NOT NULL CHECK(node_count>=0), edge_count INTEGER NOT NULL CHECK(edge_count>=0),
 density_count INTEGER NOT NULL CHECK(density_count>=0), payload_hash TEXT NOT NULL CHECK(payload_hash ~ '^sha256:[0-9a-f]{64}$'),
 payload_bytes BYTEA, storage_key TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id),
 UNIQUE(snapshot_id,slice_key,cursor_key), FOREIGN KEY(workspace_id,session_id,snapshot_id)
 REFERENCES research_projection_snapshot(workspace_id,session_id,id) ON DELETE CASCADE,
 CHECK((payload_bytes IS NULL) <> (storage_key IS NULL))
);

