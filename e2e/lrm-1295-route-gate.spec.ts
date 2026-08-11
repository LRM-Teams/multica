import { test, expect, type Page } from "@playwright/test";
import pg from "pg";
import { TestApiClient } from "./fixtures";

const databaseUrl = process.env.DATABASE_URL!;
const apiBase = process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;
const email = `lrm-1295-gate-${Date.now()}@multica.ai`;
let api: TestApiClient;
let slug = "";
let workspaceId = "";
let sessionId = "";

async function db(sql: string, params: unknown[] = []) {
  const client = new pg.Client(databaseUrl);
  await client.connect();
  try {
    return await client.query(sql, params);
  } finally {
    await client.end();
  }
}

async function authenticate(page: Page) {
  await page.goto("/login");
  await page.evaluate((token) => localStorage.setItem("multica_token", token), api.getToken());
}

test.describe.skip("LRM-1295 aggregate tree route gate — superseded by e2e/d5-constellation-session.spec.ts", () => {
  test.setTimeout(90000);
  test.beforeAll(async () => {
    api = new TestApiClient();
    await api.login(email, "LRM 1295 Gate");
    const workspace = await api.ensureWorkspace("LRM 1295 Gate", `lrm-1295-${Date.now()}`);
    slug = workspace.slug;
    workspaceId = workspace.id;
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
                 'public', 'LRM 1295 route gate', '{}'::jsonb, now())`,
      [workspaceId, `lrm-1295-runtime-${Date.now()}`],
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
       VALUES ($1, $2, $3, 'Aggregate route gate', 'Verify the aggregate tree route', 'running', 's2_sources')
       RETURNING id`,
      [workspaceId, fleet.rows[0].id, user.rows[0].id],
    );
    sessionId = session.rows[0].id;

    const nodeRows = [
      ["Root strategy", "goal", { theme_key: "strategy", assessment: "pending_review" }],
      ["Evidence branch", "probe", { theme_key: "evidence", assessment: "trusted" }],
      ["Risk branch", "conflict", { theme_key: "risk", assessment: "detour" }],
      ["Evidence leaf", "finding", { theme_key: "evidence", assessment: "trusted" }],
    ];
    const ids: string[] = [];
    for (const [title, type, payload] of nodeRows) {
      const row = await db(
        `INSERT INTO research_graph_node (workspace_id, session_id, node_type, title, summary, status, payload)
         VALUES ($1, $2, $3, $4, '', 'active', $5::jsonb) RETURNING id`,
        [workspaceId, sessionId, type, title, JSON.stringify(payload)],
      );
      ids.push(row.rows[0].id);
    }
    await db(
      `INSERT INTO research_graph_edge (workspace_id, session_id, from_node_id, to_node_id, edge_type)
       VALUES ($1, $2, $3, $4, 'leads_to'), ($1, $2, $3, $5, 'leads_to'), ($1, $2, $4, $6, 'leads_to')`,
      [workspaceId, sessionId, ids[0], ids[1], ids[2], ids[3]],
    );
  });

  test.afterAll(async () => {
    if (sessionId) await db("DELETE FROM research_session WHERE id = $1", [sessionId]);
  });

  test("desktop and narrow full-page route captures", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await authenticate(page);
    await page.goto(`/${slug}/research/${sessionId}`, { waitUntil: "commit", timeout: 60000 });
    await expect(page.getByText("Root strategy").first()).toBeVisible({ timeout: 60000 });
    await expect(page.getByText("Evidence leaf").first()).toBeVisible();
    await expect(page.getByText("Left → right · Aggregate tree")).toBeVisible();
    await expect(page.getByText("Top → bottom · Git lanes")).toHaveCount(0);
    const desktopTiers = await page.locator("[data-aggregate-tier]").evaluateAll((elements) =>
      elements.map((element) => {
        const box = element.getBoundingClientRect();
        return {
          tier: element.getAttribute("data-aggregate-tier"),
          x: box.x,
          y: box.y,
          width: box.width,
          height: box.height,
        };
      }),
    );
    const parent = desktopTiers.find((item) => item.tier === "parent")!;
    const siblings = desktopTiers.filter((item) => item.tier === "sibling");
    const children = desktopTiers.filter((item) => item.tier === "child");
    expect(parent.width).toBeGreaterThan(Math.max(...siblings.map((item) => item.width)));
    expect(Math.min(...siblings.map((item) => item.width))).toBeGreaterThan(
      Math.max(...children.map((item) => item.width)),
    );
    expect(parent.x).toBeLessThan(Math.min(...siblings.map((item) => item.x)));
    expect(Math.max(...siblings.map((item) => item.x))).toBeLessThan(
      Math.min(...children.map((item) => item.x)),
    );

    const canvasBox = await page
      .locator('[data-testid="research-canvas-overlay-grid"][data-overlay="desktop"]')
      .boundingBox();
    expect(canvasBox).not.toBeNull();
    const treeTop = Math.min(...desktopTiers.map((item) => item.y));
    const treeBottom = Math.max(...desktopTiers.map((item) => item.y + item.height));
    expect((treeBottom - treeTop) / canvasBox!.height).toBeGreaterThanOrEqual(0.58);

    await page.screenshot({ path: "e2e/artifacts/lrm-1295-route-1440.png", fullPage: true });

    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await expect(page.getByText("Root strategy").first()).toBeVisible({ timeout: 30000 });
    await expect(page.getByText("Evidence leaf").first()).toBeVisible();
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
      .toBe(true);
    await page.screenshot({ path: "e2e/artifacts/lrm-1295-route-390.png", fullPage: true });
  });
});
