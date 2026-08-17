CREATE TABLE graph_memory_profile (
  workspace_id UUID PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
  reviewer_type TEXT NOT NULL DEFAULT 'legacy'
    CHECK (reviewer_type IN ('legacy', 'graph')),
  explore_agents INT NOT NULL DEFAULT 4,
  explore_max_rounds INT NOT NULL DEFAULT 3,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
