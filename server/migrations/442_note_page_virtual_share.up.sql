CREATE TABLE note_page_share_agent (
    page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES "user"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, agent_id)
);

CREATE INDEX note_page_share_agent_agent_idx
    ON note_page_share_agent(agent_id, page_id);

CREATE TABLE note_page_share_channel (
    page_id UUID NOT NULL REFERENCES note_page(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES "user"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, channel_id)
);

CREATE INDEX note_page_share_channel_channel_idx
    ON note_page_share_channel(channel_id, page_id);
