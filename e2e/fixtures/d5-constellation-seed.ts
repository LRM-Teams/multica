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
  nodeTitles: {
    goal: string;
    stable: string;
    probe: string;
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

/** Seed a minimal typed graph that renders the D5 star-map session canvas. */
export async function seedD5ConstellationSession(
  label: string,
): Promise<D5ConstellationSeed> {
  const stamp = Date.now();
  const email = `d5-constellation-${stamp}@multica.ai`;
  const api = new TestApiClient();
  await api.login(email, label);
  const workspace = await api.ensureWorkspace(label, `d5-constellation-${stamp}`);
  const slug = workspace.slug;
  const workspaceId = workspace.id;

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
    [workspaceId, `d5-runtime-${stamp}`, label],
  );

  const warm = await fetch(`${apiBase}/api/research/sessions`, {
    headers: {
      Authorization: `Bearer ${api.getToken()}`,
      "X-Workspace-Slug": slug,
    },
  });
  if (!warm.ok) throw new Error(`fleet warm-up failed: ${warm.status}`);

  const fleet = await db("SELECT id FROM research_fleet WHERE workspace_id = $1", [workspaceId]);
  const user = await db('SELECT id FROM "user" WHERE email = $1', [email]);
  const session = await db(
    `INSERT INTO research_session (workspace_id, fleet_id, created_by, title, goal, status, current_stage)
     VALUES ($1, $2, $3, $4, $5, 'running', 's2_sources')
     RETURNING id`,
    [
      workspaceId,
      fleet.rows[0].id,
      user.rows[0].id,
      "D5 constellation gate",
      "Verify the D5 star-map session canvas",
    ],
  );
  const sessionId = session.rows[0].id as string;

  const nodeTitles = {
    goal: "D5 research goal",
    stable: "Stable synthesis result",
    probe: "Active probe direction",
  };

  const insertNode = async (
    title: string,
    nodeType: string,
    level: string,
    status: string,
    round = 1,
  ) => {
    const row = await db(
      `INSERT INTO research_graph_node (
         workspace_id, session_id, node_type, title, summary, status,
         level, round, document_count, conclusion_count, payload
       ) VALUES ($1, $2, $3, $4, '', $5, $6, $7, 3, 1, '{}'::jsonb)
       RETURNING id`,
      [workspaceId, sessionId, nodeType, title, status, level, round],
    );
    return row.rows[0].id as string;
  };

  const goalId = await insertNode(nodeTitles.goal, "goal", "XXL", "active");
  const stableId = await insertNode(nodeTitles.stable, "finding", "L", "done", 2);
  const probeId = await insertNode(nodeTitles.probe, "probe", "S", "active", 2);

  await db(
    `INSERT INTO research_graph_edge (workspace_id, session_id, from_node_id, to_node_id, edge_type)
     VALUES
       ($1, $2, $3, $4, 'leads_to'),
       ($1, $2, $3, $5, 'leads_to'),
       ($1, $2, $4, $5, 'supports')`,
    [workspaceId, sessionId, goalId, stableId, probeId],
  );

  await db(
    `UPDATE research_session SET graph_version = 1 WHERE id = $1 AND workspace_id = $2`,
    [sessionId, workspaceId],
  );

  return { api, slug, workspaceId, sessionId, nodeTitles };
}

export async function deleteD5ConstellationSession(sessionId: string) {
  if (!sessionId) return;
  await db("DELETE FROM research_session WHERE id = $1", [sessionId]);
}
