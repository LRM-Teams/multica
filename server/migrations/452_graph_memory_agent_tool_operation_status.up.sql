ALTER TABLE graph_memory_agent_tool_operation
  ADD COLUMN status text NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'completed', 'failed')),
  ADD COLUMN completed_at timestamptz;
