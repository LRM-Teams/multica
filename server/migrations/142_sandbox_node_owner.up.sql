ALTER TABLE sandbox_node
    ADD COLUMN owner_user_id UUID REFERENCES "user"(id) ON DELETE CASCADE,
    ADD COLUMN deleted_at TIMESTAMPTZ;

CREATE INDEX sandbox_node_owner_idx ON sandbox_node(owner_user_id, created_at) WHERE deleted_at IS NULL;
