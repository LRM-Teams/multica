-- LRM-1505 调研星图 D5/BE：类型化图模型、成果融合与完整谱系。
--
-- 目标：把 research_graph_node 从「前端约定散乱 JSON」升级为可查询、可持久化、
-- 可审计的类型化图模型，同时保持旧 session/payload 无损兼容。
--
-- 内容：
--   1) research_graph_cluster：类型化集群（星图上用于把节点聚合为可折叠的一簇）。
--   2) research_graph_node 新增稳定字面量字段与谱系字段（含融合用的 merged_from 数组）。
--   3) 扩展 node_type / status / edge_type 枚举（新增 conclusion、superseded 等）。
--   4) research_session 新增 graph_version：每次星图变更在同一事务内自增，
--      用于 research_session:graph_updated 的 version 字段与前端 invalidation。
--   5) research_graph_command：融合/废弃/重启/补强/新前沿 的幂等键 + 审计记录。

-- 1) 集群表 ----------------------------------------------------------------
CREATE TABLE research_graph_cluster (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  name TEXT NOT NULL DEFAULT '',
  label TEXT NOT NULL DEFAULT '',
  level TEXT NOT NULL DEFAULT 'M'
    CHECK (level IN ('XXL', 'XL', 'L', 'M', 'S')),
  cluster_type TEXT NOT NULL DEFAULT 'theme',
  goal_version_id UUID,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX research_graph_cluster_session_idx
  ON research_graph_cluster (session_id, created_at);

-- 2) 节点稳定字段 + 谱系字段 ------------------------------------------------
ALTER TABLE research_graph_node
  ADD COLUMN level TEXT NOT NULL DEFAULT 'M'
    CHECK (level IN ('XXL', 'XL', 'L', 'M', 'S')),
  ADD COLUMN round INTEGER NOT NULL DEFAULT 1 CHECK (round >= 1),
  ADD COLUMN cluster_id UUID REFERENCES research_graph_cluster(id) ON DELETE SET NULL,
  ADD COLUMN confidence DOUBLE PRECISION
    CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
  ADD COLUMN document_count INTEGER NOT NULL DEFAULT 0 CHECK (document_count >= 0),
  ADD COLUMN conclusion_count INTEGER NOT NULL DEFAULT 0 CHECK (conclusion_count >= 0),
  ADD COLUMN goal_version_id UUID,
  -- 谱系：单父语义用外键；融合输入用数组（多对一到融合结论）。
  ADD COLUMN derived_from UUID REFERENCES research_graph_node(id) ON DELETE SET NULL,
  ADD COLUMN merged_from UUID[] NOT NULL DEFAULT '{}',
  ADD COLUMN superseded_by UUID REFERENCES research_graph_node(id) ON DELETE SET NULL,
  ADD COLUMN restart_of UUID REFERENCES research_graph_node(id) ON DELETE SET NULL,
  ADD COLUMN invalidated_by UUID REFERENCES research_graph_node(id) ON DELETE SET NULL,
  ADD COLUMN superseded_at TIMESTAMPTZ,
  ADD COLUMN invalidated_at TIMESTAMPTZ;

CREATE INDEX research_graph_node_cluster_idx
  ON research_graph_node (session_id, cluster_id);
CREATE INDEX research_graph_node_round_idx
  ON research_graph_node (session_id, round);

-- 3) 扩展枚举 ---------------------------------------------------------------
ALTER TABLE research_graph_node DROP CONSTRAINT research_graph_node_node_type_check;
ALTER TABLE research_graph_node ADD CONSTRAINT research_graph_node_node_type_check
  CHECK (node_type IN (
    'goal', 'subquestion', 'probe', 'finding', 'conflict', 'dead_end',
    'refuted', 'pivot', 'roster_change', 'stage_gate', 'agent_activity',
    'conclusion'
  ));

ALTER TABLE research_graph_node DROP CONSTRAINT research_graph_node_status_check;
ALTER TABLE research_graph_node ADD CONSTRAINT research_graph_node_status_check
  CHECK (status IN (
    'active', 'done', 'abandoned', 'superseded', 'invalidated', 'restarted', 'deprecated'
  ));

ALTER TABLE research_graph_edge DROP CONSTRAINT research_graph_edge_edge_type_check;
ALTER TABLE research_graph_edge ADD CONSTRAINT research_graph_edge_edge_type_check
  CHECK (edge_type IN (
    'leads_to', 'supports', 'contradicts', 'supersedes', 'abandons',
    'derived_from', 'merged_from', 'superseded_by', 'deepens', 'restart_of', 'invalidated_by'
  ));

-- 4) 会话图版本 -------------------------------------------------------------
ALTER TABLE research_session ADD COLUMN graph_version BIGINT NOT NULL DEFAULT 0 CHECK (graph_version >= 0);

-- 5) 图操作命令审计 + 幂等键 -------------------------------------------------
CREATE TABLE research_graph_command (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  op TEXT NOT NULL,
  idempotency_key TEXT NOT NULL DEFAULT '',
  result_node_id UUID,
  input_node_ids UUID[] NOT NULL DEFAULT '{}',
  reason TEXT NOT NULL DEFAULT '',
  actor_type TEXT NOT NULL DEFAULT 'user',
  actor_id UUID,
  meta JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 幂等键在 (workspace, session) 域内唯一：重复提交同一融合命令不会产生第二个节点。
CREATE UNIQUE INDEX research_graph_command_idem_ws
  ON research_graph_command (workspace_id, session_id, idempotency_key)
  WHERE idempotency_key <> '';

CREATE INDEX research_graph_command_session_idx
  ON research_graph_command (session_id, created_at);
