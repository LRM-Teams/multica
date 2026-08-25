import { test, expect } from "@playwright/test";
import pg from "pg";
import { TestApiClient } from "./fixtures";

// V6 HTTP lifecycle contract: bootstrap a Director-led run over the public
// API, read the Director projection snapshot, then archive it while retaining
// canonical research facts. Agent execution is covered separately because it
// requires a real daemon/provider rather than a synthetic online DB row.

const API_BASE =
  process.env.NEXT_PUBLIC_API_URL ||
  `http://localhost:${process.env.PORT || "8080"}`;
const DATABASE_URL =
  process.env.DATABASE_URL ??
  "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

const E2E_EMAIL = `e2e-research-v6-${Date.now()}@multica.ai`;
const WS_SLUG = `e2e-research-v6-ws-${Date.now()}`;

let api: TestApiClient;
let slug: string;
let workspaceId = "";
let directorAgentId = "";

async function dbQuery(sql: string, params: unknown[] = []) {
  const client = new pg.Client(DATABASE_URL);
  await client.connect();
  try {
    return await client.query(sql, params);
  } catch (error) {
    const databaseError = error as pg.DatabaseError;
    throw new Error(
      [databaseError.message, databaseError.detail, databaseError.constraint]
        .filter(Boolean)
        .join(": "),
    );
  } finally {
    await client.end();
  }
}

async function authedFetch(path: string, init?: RequestInit) {
  return fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${api.getToken()}`,
      "X-Workspace-Slug": slug,
      ...(init?.headers ?? {}),
    },
  });
}

test.beforeAll(async () => {
  api = new TestApiClient();
  await api.login(E2E_EMAIL, "E2E Research V6");
  const ws = await api.ensureWorkspace("E2E Research V6 Workspace", WS_SLUG);
  slug = ws.slug;
  workspaceId = ws.id;
  await api.ensureWorkspaceReady(ws);

  const runtime = await dbQuery(
    `INSERT INTO agent_runtime (
       workspace_id, daemon_id, name, runtime_mode, provider, status,
       visibility, device_info, metadata, last_seen_at
     )
     VALUES ($1, NULL, $2, 'cloud', 'e2e_v6_runtime', 'online',
             'public', 'E2E V6 runtime',
             '{"capabilities":["research_run_v6_v1"]}'::jsonb, now())
     RETURNING id`,
    [workspaceId, `e2e v6 runtime ${Date.now()}`],
  );
  const director = await dbQuery(
    `INSERT INTO agent (
       workspace_id, name, description, runtime_mode, runtime_config,
       runtime_id, model, max_concurrent_tasks, owner_id
     )
     VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'e2e-model', 1, $4)
     RETURNING id`,
    [workspaceId, `E2E V6 Director ${Date.now()}`, runtime.rows[0].id, api.getUserId()],
  );
  directorAgentId = director.rows[0].id as string;
});

test.describe.serial("research V6 HTTP lifecycle — create, snapshot, archive", () => {
  let runId = "";

  test("bootstrap a V6 run with an explicit Director", async () => {
    const res = await authedFetch("/api/research/sessions", {
      method: "POST",
      body: JSON.stringify({
        goal: "E2E V6 closed loop goal",
        title: "E2E V6 closed loop",
        depth_tier: "shallow",
        orchestrator_version: "research-run-v6",
        director_agent_id: directorAgentId,
        client_request_id: crypto.randomUUID(),
      }),
    });
    expect(res.status, await res.clone().text()).toBe(201);
    const body = (await res.json()) as { session: { id: string; orchestrator_version?: string } };
    runId = body.session.id;
    expect(runId).toMatch(/[0-9a-f-]{36}/);

    const row = await dbQuery(
      `SELECT orchestrator_version FROM research_session WHERE id = $1 AND workspace_id = $2`,
      [runId, workspaceId],
    );
    expect(row.rows[0]?.orchestrator_version).toBe("research-run-v6");
  });

  test("director projection snapshot is served in the frozen contract shape", async () => {
    const res = await authedFetch(
      `/api/research/v6/runs/${runId}/projection/snapshot`,
    );
    expect(res.status, await res.clone().text()).toBe(200);
    const snapshot = (await res.json()) as {
      contract_kind: string;
      run_id: string;
      projection_hash: string;
      slice_key: string;
      nodes: Array<{ id: string; kind: string }>;
    };
    expect(snapshot.contract_kind).toBe("projection_snapshot");
    expect(snapshot.run_id).toBe(runId);
    expect(snapshot.projection_hash).toMatch(/^sha256:[0-9a-f]{64}$/);
    expect(snapshot.slice_key).toBeTruthy();
    // A freshly bootstrapped run must already project its goal node.
    expect(snapshot.nodes.some((node) => node.kind === "goal")).toBe(true);
  });

  test("delete archives the run and retains canonical integration facts", async () => {
    const round = await dbQuery(
      `INSERT INTO research_integration_round (
         workspace_id, session_id, trigger_kind, input_event_sequence,
         input_state_hash, goal_version, plan_version
       )
       VALUES ($1, $2, 'manual', 0,
               'sha256:${"a".repeat(64)}', 1, 1)
       RETURNING id`,
      [workspaceId, runId],
    );
    await dbQuery(
      `INSERT INTO research_integration_contribution (
         workspace_id, session_id, integration_round_id, author_agent_id, compared_artifact_ids
       )
       VALUES ($1, $2, $3, $4, '["e2e-artifact"]'::jsonb)`,
      [workspaceId, runId, round.rows[0].id, directorAgentId],
    );

    const res = await authedFetch(`/api/research/sessions/${runId}`, {
      method: "DELETE",
    });
    expect(res.status, await res.clone().text()).toBe(204);

    const session = await dbQuery(
      `SELECT status
       FROM research_session WHERE id = $1 AND workspace_id = $2`,
      [runId, workspaceId],
    );
    expect(session.rows[0]?.status).toBe("archived");
    const rounds = await dbQuery(
      `SELECT count(*)::int AS n FROM research_integration_round
       WHERE session_id = $1 AND workspace_id = $2`,
      [runId, workspaceId],
    );
    expect(rounds.rows[0].n).toBe(1);
  });
});
