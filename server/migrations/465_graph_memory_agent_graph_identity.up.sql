-- Research federation (unification spec §4.4): the Agent gateway serves both
-- the channel-route graph and the workspace research graph, so durable agent
-- rows must record WHICH graph an operation or citation belongs to.
-- Existing rows keep '' (= the channel-route graph).

ALTER TABLE graph_memory_agent_tool_operation
  ADD COLUMN graph_identity text NOT NULL DEFAULT '';

ALTER TABLE graph_memory_agent_citation
  ADD COLUMN graph_identity text NOT NULL DEFAULT '';

-- Idempotency is per graph: the same client key may reserve one operation on
-- each graph under one trajectory.
ALTER TABLE graph_memory_agent_tool_operation
  DROP CONSTRAINT graph_memory_agent_tool_opera_trajectory_id_idempotency_key_key;
ALTER TABLE graph_memory_agent_tool_operation
  ADD CONSTRAINT graph_memory_agent_tool_operation_traj_graph_idem_key
  UNIQUE (trajectory_id, graph_identity, idempotency_key);

-- Citations are graph-qualified: identical node ids from two graphs never
-- collapse.
ALTER TABLE graph_memory_agent_citation
  DROP CONSTRAINT graph_memory_agent_citation_trajectory_id_node_id_key;
ALTER TABLE graph_memory_agent_citation
  ADD CONSTRAINT graph_memory_agent_citation_traj_graph_node_key
  UNIQUE (trajectory_id, graph_identity, node_id);
