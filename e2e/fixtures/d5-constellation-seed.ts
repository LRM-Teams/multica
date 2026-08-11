import pg from "pg";
import { TestApiClient } from "../fixtures";

const databaseUrl = process.env.DATABASE_URL!;
const apiBase =
  process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;

export type D5ConstellationSeed = {
  api: TestApiClient;
  slug: string;
  workspaceId: string;
  sessionId: string;
  fleetAgentId?: string;
  nodeTitles: {
    goal: string;
    stable: string;
    probe: string;
    prior?: string;
  };
};

async function db(sql: string, params: unknown[] = []) {
  const client = new pg.Client(databaseUrl);
  await client.connect();
  try {
    return await client.query(sql, params);
  } finally {
    await client.end();
  }
}

async function setupD5Workspace(label: string, slugPrefix: string) {
  const stamp = Date.now();
  const email = `${slugPrefix}-${stamp}@multica.ai`;
  const api = new TestApiClient();
  await api.login(email, label);
  const workspace = await api.ensureWorkspace(label, `${slugPrefix}-${stamp}`);

  await db(
    `UPDATE "user"
     SET onboarded_at = now(),
         onboarding_questionnaire = '{"source":["other"],"source_skipped":false}'::jsonb
     WHERE email = $1`,
    [email],
  );
  await db(
    `INSERT INTO agent_runtime (
       workspace_id, daemon_id, name, runtime_mode, provider, status,
       visibility, device_info, metadata, last_seen_at
     ) VALUES ($1, NULL, $2, 'cloud', 'e2e_research_runtime', 'online',
               'public', $3, '{}'::jsonb, now())`,
    [workspace.id, `${slugPrefix}-runtime-${stamp}`, label],
  );

  const warm = await fetch(`${apiBase}/api/research/sessions`, {
    headers: {
      Authorization: `Bearer ${api.getToken()}`,
      "X-Workspace-Slug": workspace.slug,
    },
  });
  if (!warm.ok) throw new Error(`fleet warm-up failed: ${warm.status}`);

  const fleet = await db("SELECT id FROM research_fleet WHERE workspace_id = $1", [workspace.id]);
  const fleetId = fleet.rows[0].id as string;
  const agentRow = await db(
    `SELECT agent_id FROM research_fleet_member
     WHERE fleet_id = $1 AND status <> 'archived'
     ORDER BY created_at ASC
     LIMIT 1`,
    [fleetId],
  );
  const user = await db('SELECT id FROM "user" WHERE email = $1', [email]);

  return {
    api,
    slug: workspace.slug,
    workspaceId: workspace.id,
    fleetId,
    fleetAgentId: agentRow.rows[0]?.agent_id as string | undefined,
    userId: user.rows[0].id as string,
  };
}

/** Seed a minimal typed graph that renders the D5 star-map session canvas. */
export async function seedD5ConstellationSession(
  label: string,
): Promise<D5ConstellationSeed> {
  const workspace = await setupD5Workspace(label, "d5-constellation");
  const session = await db(
    `INSERT INTO research_session (workspace_id, fleet_id, created_by, title, goal, status, current_stage)
     VALUES ($1, $2, $3, $4, $5, 'running', 's2_sources')
     RETURNING id`,
    [
      workspace.workspaceId,
      workspace.fleetId,
      workspace.userId,
      "D5 constellation gate",
      "Verify the D5 star-map session canvas",
    ],
  );
  const sessionId = session.rows[0].id as string;

  const nodeTitles = {
    goal: "D5 research goal",
    prior: "Prior synthesis draft",
    stable: "Stable synthesis result",
    probe: "Active probe direction",
  };

  const goalRow = await db(
    `INSERT INTO research_graph_node (
       workspace_id, session_id, node_type, title, summary, status,
       level, round, document_count, conclusion_count, payload
     ) VALUES ($1, $2, 'goal', $3, '', 'active', 'XXL', 1, 3, 1, '{}'::jsonb)
     RETURNING id`,
    [workspace.workspaceId, sessionId, nodeTitles.goal],
  );
  const goalId = goalRow.rows[0].id as string;

  const priorRow = await db(
    `INSERT INTO research_graph_node (
       workspace_id, session_id, node_type, title, summary, status,
       level, round, document_count, conclusion_count, payload
     ) VALUES ($1, $2, 'finding', $3, '', 'superseded', 'M', 1, 2, 1, '{}'::jsonb)
     RETURNING id`,
    [workspace.workspaceId, sessionId, nodeTitles.prior],
  );
  const priorId = priorRow.rows[0].id as string;

  const stableRow = await db(
    `INSERT INTO research_graph_node (
       workspace_id, session_id, node_type, title, summary, status,
       level, round, document_count, conclusion_count, merged_from, payload
     ) VALUES ($1, $2, 'finding', $3, '', 'done', 'L', 2, 3, 1, $4, '{}'::jsonb)
     RETURNING id`,
    [workspace.workspaceId, sessionId, nodeTitles.stable, [priorId]],
  );
  const stableId = stableRow.rows[0].id as string;

  const probeRow = await db(
    `INSERT INTO research_graph_node (
       workspace_id, session_id, node_type, title, summary, status,
       level, round, actor_agent_id, document_count, conclusion_count, payload
     ) VALUES ($1, $2, 'probe', $3, '', 'active', 'S', 2, $4, 1, 0, '{}'::jsonb)
     RETURNING id`,
    [workspace.workspaceId, sessionId, nodeTitles.probe, workspace.fleetAgentId ?? null],
  );
  const probeId = probeRow.rows[0].id as string;

  await db(
    `INSERT INTO research_graph_edge (workspace_id, session_id, from_node_id, to_node_id, edge_type)
     VALUES
       ($1, $2, $3, $4, 'leads_to'),
       ($1, $2, $3, $5, 'leads_to'),
       ($1, $2, $3, $6, 'leads_to'),
       ($1, $2, $5, $6, 'supports'),
       ($1, $2, $4, $5, 'merged_from')`,
    [workspace.workspaceId, sessionId, goalId, priorId, stableId, probeId],
  );

  await db(
    `UPDATE research_session SET graph_version = 1 WHERE id = $1 AND workspace_id = $2`,
    [sessionId, workspace.workspaceId],
  );

  return {
    api: workspace.api,
    slug: workspace.slug,
    workspaceId: workspace.workspaceId,
    sessionId,
    fleetAgentId: workspace.fleetAgentId,
    nodeTitles,
  };
}

/** Goal-only session while the graph is still forming. */
export async function seedD5FormingConstellationSession(label: string): Promise<D5ConstellationSeed> {
  const workspace = await setupD5Workspace(label, "d5-forming");
  const session = await db(
    `INSERT INTO research_session (workspace_id, fleet_id, created_by, title, goal, status, current_stage)
     VALUES ($1, $2, $3, $4, $5, 'running', 's2_sources')
     RETURNING id`,
    [
      workspace.workspaceId,
      workspace.fleetId,
      workspace.userId,
      "D5 forming gate",
      "Waiting for the first research branches",
    ],
  );
  const sessionId = session.rows[0].id as string;

  return {
    api: workspace.api,
    slug: workspace.slug,
    workspaceId: workspace.workspaceId,
    sessionId,
    fleetAgentId: workspace.fleetAgentId,
    nodeTitles: {
      goal: "Waiting for the first research branches",
      stable: "",
      probe: "",
    },
  };
}

/** Nodes with intentionally missing metrics — UI must not invent zero placeholders. */
export async function seedD5SparseConstellationSession(label: string): Promise<D5ConstellationSeed> {
  const workspace = await setupD5Workspace(label, "d5-sparse");
  const session = await db(
    `INSERT INTO research_session (workspace_id, fleet_id, created_by, title, goal, status, current_stage)
     VALUES ($1, $2, $3, $4, $5, 'running', 's2_sources')
     RETURNING id`,
    [
      workspace.workspaceId,
      workspace.fleetId,
      workspace.userId,
      "D5 sparse metrics gate",
      "Verify missing metric fields stay hidden",
    ],
  );
  const sessionId = session.rows[0].id as string;

  const nodeTitles = {
    goal: "Sparse metrics goal",
    stable: "Sparse metrics finding",
    probe: "Sparse metrics probe",
  };

  const goalRow = await db(
    `INSERT INTO research_graph_node (
       workspace_id, session_id, node_type, title, summary, status,
       level, round, payload
     ) VALUES ($1, $2, 'goal', $3, '', 'active', 'XXL', 1, '{}'::jsonb)
     RETURNING id`,
    [workspace.workspaceId, sessionId, nodeTitles.goal],
  );
  const goalId = goalRow.rows[0].id as string;

  const stableRow = await db(
    `INSERT INTO research_graph_node (
       workspace_id, session_id, node_type, title, summary, status,
       level, round, confidence, document_count, conclusion_count, payload
     ) VALUES ($1, $2, 'finding', $3, '', 'done', 'L', 1, NULL, 0, 0, '{}'::jsonb)
     RETURNING id`,
    [workspace.workspaceId, sessionId, nodeTitles.stable],
  );

  await db(
    `INSERT INTO research_graph_edge (workspace_id, session_id, from_node_id, to_node_id, edge_type)
     VALUES ($1, $2, $3, $4, 'leads_to')`,
    [workspace.workspaceId, sessionId, goalId, stableRow.rows[0].id],
  );

  await db(
    `UPDATE research_session SET graph_version = 1 WHERE id = $1 AND workspace_id = $2`,
    [sessionId, workspace.workspaceId],
  );

  return {
    api: workspace.api,
    slug: workspace.slug,
    workspaceId: workspace.workspaceId,
    sessionId,
    fleetAgentId: workspace.fleetAgentId,
    nodeTitles,
  };
}

export async function deleteD5ConstellationSession(sessionId: string) {
  if (!sessionId) return;
  await db("DELETE FROM research_session WHERE id = $1", [sessionId]);
}

/** Seed ~30 nodes across two clusters for low-zoom budget / collapse gates. */
export async function seedD5LargeConstellationSession(
  label: string,
): Promise<D5ConstellationSeed & { clusterIds: [string, string] }> {
  const workspace = await setupD5Workspace(label, "d5-large");
  const session = await db(
    `INSERT INTO research_session (workspace_id, fleet_id, created_by, title, goal, status, current_stage)
     VALUES ($1, $2, $3, $4, $5, 'running', 's2_sources')
     RETURNING id`,
    [
      workspace.workspaceId,
      workspace.fleetId,
      workspace.userId,
      "D5 large constellation gate",
      "Stress the D5 star-map DOM budget and cluster collapse",
    ],
  );
  const sessionId = session.rows[0].id as string;

  const clusterA = await db(
    `INSERT INTO research_graph_cluster (workspace_id, session_id, name, label, level)
     VALUES ($1, $2, 'regulatory', 'Regulatory cluster', 'L')
     RETURNING id`,
    [workspace.workspaceId, sessionId],
  );
  const clusterB = await db(
    `INSERT INTO research_graph_cluster (workspace_id, session_id, name, label, level)
     VALUES ($1, $2, 'cost', 'Cost cluster', 'L')
     RETURNING id`,
    [workspace.workspaceId, sessionId],
  );
  const clusterAId = clusterA.rows[0].id as string;
  const clusterBId = clusterB.rows[0].id as string;

  const nodeTitles = {
    goal: "D5 large research goal",
    stable: "Round-two synthesis",
    probe: "Round-two probe",
  };

  const goalRow = await db(
    `INSERT INTO research_graph_node (
       workspace_id, session_id, node_type, title, summary, status,
       level, round, document_count, conclusion_count, payload
     ) VALUES ($1, $2, 'goal', $3, '', 'active', 'XXL', 1, 5, 2, '{}'::jsonb)
     RETURNING id`,
    [workspace.workspaceId, sessionId, nodeTitles.goal],
  );
  const goalId = goalRow.rows[0].id as string;

  const insertClusterNode = async (title: string, clusterId: string, round: number) => {
    const row = await db(
      `INSERT INTO research_graph_node (
         workspace_id, session_id, node_type, title, summary, status,
         level, round, cluster_id, document_count, conclusion_count, payload
       ) VALUES ($1, $2, 'finding', $3, '', 'done', 'M', $4, $5, 2, 1, '{}'::jsonb)
       RETURNING id`,
      [workspace.workspaceId, sessionId, title, round, clusterId],
    );
    const nodeId = row.rows[0].id as string;
    await db(
      `INSERT INTO research_graph_edge (workspace_id, session_id, from_node_id, to_node_id, edge_type)
       VALUES ($1, $2, $3, $4, 'leads_to')`,
      [workspace.workspaceId, sessionId, goalId, nodeId],
    );
    return nodeId;
  };

  for (let i = 0; i < 14; i += 1) {
    await insertClusterNode(`Regulatory finding ${i + 1}`, clusterAId, i % 2 === 0 ? 1 : 2);
  }
  for (let i = 0; i < 14; i += 1) {
    await insertClusterNode(`Cost finding ${i + 1}`, clusterBId, i % 2 === 0 ? 1 : 2);
  }

  const stableRow = await db(
    `INSERT INTO research_graph_node (
       workspace_id, session_id, node_type, title, summary, status,
       level, round, document_count, conclusion_count, payload
     ) VALUES ($1, $2, 'finding', $3, '', 'done', 'L', 2, 4, 2, '{}'::jsonb)
     RETURNING id`,
    [workspace.workspaceId, sessionId, nodeTitles.stable],
  );
  const probeRow = await db(
    `INSERT INTO research_graph_node (
       workspace_id, session_id, node_type, title, summary, status,
       level, round, document_count, conclusion_count, payload
     ) VALUES ($1, $2, 'probe', $3, '', 'active', 'S', 2, 1, 0, '{}'::jsonb)
     RETURNING id`,
    [workspace.workspaceId, sessionId, nodeTitles.probe],
  );
  await db(
    `INSERT INTO research_graph_edge (workspace_id, session_id, from_node_id, to_node_id, edge_type)
     VALUES ($1, $2, $3, $4, 'supports')`,
    [workspace.workspaceId, sessionId, stableRow.rows[0].id, probeRow.rows[0].id],
  );

  await db(
    `UPDATE research_session SET graph_version = 1 WHERE id = $1 AND workspace_id = $2`,
    [sessionId, workspace.workspaceId],
  );

  return {
    api: workspace.api,
    slug: workspace.slug,
    workspaceId: workspace.workspaceId,
    sessionId,
    fleetAgentId: workspace.fleetAgentId,
    nodeTitles,
    clusterIds: [clusterAId, clusterBId],
  };
}
