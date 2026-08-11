import { expect, test, type Page } from "@playwright/test";
import pg from "pg";
import { TestApiClient } from "./fixtures";
import { createLrm1385AggregateFixture } from "./fixtures/lrm-1385-aggregate-tree";

const fixture = createLrm1385AggregateFixture();
const databaseUrl = process.env.DATABASE_URL!;
const apiBase =
  process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;
const runIntegration = process.env.LRM_1385_INTEGRATION === "1";

type SeededFixture = {
  api: TestApiClient;
  slug: string;
  sessionId: string;
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

async function authenticate(page: Page, api: TestApiClient) {
  await page.goto("/login");
  await page.evaluate((token) => localStorage.setItem("multica_token", token), api.getToken());
}

async function seedFixture(): Promise<SeededFixture> {
  const api = new TestApiClient();
  const stamp = Date.now();
  const email = `lrm-1385-gate-${stamp}@multica.ai`;
  await api.login(email, "LRM 1385 Gate");
  const workspace = await api.ensureWorkspace("LRM 1385 Gate", `lrm-1385-${stamp}`);

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
               'public', 'LRM 1385 acceptance gate', '{}'::jsonb, now())`,
    [workspace.id, `lrm-1385-runtime-${stamp}`],
  );
  const warm = await fetch(`${apiBase}/api/research/sessions`, {
    headers: {
      Authorization: `Bearer ${api.getToken()}`,
      "X-Workspace-Slug": workspace.slug,
    },
  });
  if (!warm.ok) throw new Error(`fleet warm-up failed: ${warm.status}`);

  const fleet = await db("SELECT id FROM research_fleet WHERE workspace_id = $1", [workspace.id]);
  const user = await db('SELECT id FROM "user" WHERE email = $1', [email]);
  const session = await db(
    `INSERT INTO research_session (workspace_id, fleet_id, created_by, title, goal, status, current_stage)
     VALUES ($1, $2, $3, 'LRM 1385 aggregate acceptance',
             'Verify a 96-node aggregate tree', 'running', 's2_sources')
     RETURNING id`,
    [workspace.id, fleet.rows[0].id, user.rows[0].id],
  );
  const sessionId = session.rows[0].id as string;
  const ids = new Map<string, string>();

  for (const node of fixture.nodes) {
    const row = await db(
      `INSERT INTO research_graph_node
         (workspace_id, session_id, node_type, title, summary, status, payload)
       VALUES ($1, $2, $3, $4, '', 'active', $5::jsonb)
       RETURNING id`,
      [
        workspace.id,
        sessionId,
        node.nodeType,
        node.title,
        JSON.stringify({ theme_key: node.themeKey, assessment: node.assessment }),
      ],
    );
    ids.set(node.key, row.rows[0].id);
  }
  for (const node of fixture.nodes) {
    if (!node.parentKey) continue;
    await db(
      `INSERT INTO research_graph_edge
         (workspace_id, session_id, from_node_id, to_node_id, edge_type)
       VALUES ($1, $2, $3, $4, 'leads_to')`,
      [workspace.id, sessionId, ids.get(node.parentKey), ids.get(node.key)],
    );
  }

  return { api, slug: workspace.slug, sessionId };
}

async function openFixture(page: Page, seeded: SeededFixture, width: number, height: number) {
  await page.setViewportSize({ width, height });
  await authenticate(page, seeded.api);
  await page.goto(`/${seeded.slug}/research/${seeded.sessionId}`, {
    waitUntil: "commit",
    timeout: 60_000,
  });
  await expect(page.getByText("96-node research goal").first()).toBeVisible({ timeout: 60_000 });
}

async function useTheme(page: Page, theme: "light" | "dark") {
  await page.evaluate((nextTheme) => {
    document.documentElement.classList.toggle("dark", nextTheme === "dark");
    document.documentElement.style.colorScheme = nextTheme;
  }, theme);
}

test.describe("LRM-1385 typed 96-node fixture", () => {
  test("keeps the server-shaped hierarchy and a bounded initial window", () => {
    expect(fixture.nodes).toHaveLength(96);
    expect(fixture.branchKeys).toHaveLength(8);
    expect(fixture.initialVisibleKeys.length).toBeLessThan(fixture.nodes.length);
    expect(fixture.hiddenGroupCount).toBe(7);

    const byKey = new Map(fixture.nodes.map((node) => [node.key, node]));
    for (const node of fixture.nodes) {
      expect(new Set(node.childKeys).size).toBe(node.childKeys.length);
      for (const childKey of node.childKeys) {
        expect(byKey.get(childKey)?.parentKey).toBe(node.key);
      }
    }
  });
});

test.describe.skip("LRM-1385 aggregate tree integration gate — superseded by D5 star-graph session canvas", () => {
  test.skip(!runIntegration, "Set LRM_1385_INTEGRATION=1 with the worktree stack running");
  test.setTimeout(120_000);
  let seeded: SeededFixture;

  test.beforeAll(async () => {
    seeded = await seedFixture();
  });

  test.afterAll(async () => {
    if (seeded?.sessionId) {
      await db("DELETE FROM research_session WHERE id = $1", [seeded.sessionId]);
    }
  });

  test("96 nodes use a readable bounded window on desktop", async ({ page }) => {
    await openFixture(page, seeded, 1440, 900);
    const rendered = page.locator("[data-aggregate-tier]");
    await expect(rendered.first()).toBeVisible();
    expect(await rendered.count()).toBeLessThan(fixture.nodes.length);
    await expect(page.getByText(new RegExp(`(?:remaining|其余)\\s*${fixture.hiddenGroupCount}`, "i"))).toBeVisible();

    const tiers = await rendered.evaluateAll((elements) =>
      elements.map((element) => element.getAttribute("data-aggregate-tier")),
    );
    expect(tiers).toContain("parent");
    expect(tiers).toContain("sibling");
    expect(tiers).toContain("child");
  });

  for (const width of [1440, 1024, 768] as const) {
    for (const theme of ["light", "dark"] as const) {
      test(`${width}px ${theme}: visible node click recenters without overlap`, async ({ page }) => {
        await openFixture(page, seeded, width, 900);
        await useTheme(page, theme);
        const canvas = page.locator('[data-testid="research-canvas-overlay-grid"]');
        const target = page.getByRole("button", { name: /Research group 8/i }).first();
        await target.click();
        await expect(target).toBeFocused();
        const [canvasBox, targetBox] = await Promise.all([canvas.boundingBox(), target.boundingBox()]);
        expect(canvasBox).not.toBeNull();
        expect(targetBox).not.toBeNull();
        const targetCenterX = targetBox!.x + targetBox!.width / 2;
        const targetCenterY = targetBox!.y + targetBox!.height / 2;
        expect(Math.abs(targetCenterX - (canvasBox!.x + canvasBox!.width / 2))).toBeLessThan(
          canvasBox!.width * 0.2,
        );
        expect(Math.abs(targetCenterY - (canvasBox!.y + canvasBox!.height / 2))).toBeLessThan(
          canvasBox!.height * 0.2,
        );
        await page.screenshot({
          path: `e2e/artifacts/lrm-1385-after-${width}-${theme}.png`,
          fullPage: true,
        });
      });
    }
  }

  test("keyboard expansion preserves focus, selection and breadcrumb", async ({ page }) => {
    await openFixture(page, seeded, 1024, 900);
    const branch = page.getByRole("button", { name: /Research group 2/i }).first();
    await branch.focus();
    await branch.press("Enter");
    await expect(branch).toHaveAttribute("aria-expanded", "true");
    await expect(branch).toHaveAttribute("aria-current", /true|page/);
    await expect(page.getByRole("navigation", { name: /breadcrumb|路径/i })).toContainText(
      "Research group 2",
    );
    await branch.press("Enter");
    await expect(branch).toHaveAttribute("aria-expanded", "false");
    await expect(branch).toBeFocused();
  });

  test("200% zoom remains readable and horizontally contained", async ({ page }) => {
    await openFixture(page, seeded, 768, 900);
    await page.evaluate(() => {
      document.documentElement.style.zoom = "2";
    });
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
      .toBe(true);
    await expect(page.getByText("96-node research goal").first()).toBeVisible();
  });

  test("reduced motion reaches the final state without sustained displacement", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await openFixture(page, seeded, 1440, 900);
    const target = page.getByRole("button", { name: /Research group 3/i }).first();
    await target.click();
    const first = await target.boundingBox();
    await page.waitForTimeout(80);
    const second = await target.boundingBox();
    expect(second).toEqual(first);
    await expect(page.locator('[data-reorg="running"]')).toHaveCount(0);
  });

  test("360px is a vertical three-tier tree without free-canvas dependency", async ({ page }) => {
    await openFixture(page, seeded, 360, 800);
    await expect(page.locator(".react-flow")).toHaveCount(0);
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
      .toBe(true);
    const visibleTitles = await page
      .locator("[data-aggregate-tier]")
      .evaluateAll((elements) => elements.map((element) => element.textContent ?? ""));
    expect(visibleTitles.length).toBeLessThan(fixture.nodes.length);
    await page.screenshot({
      path: "e2e/artifacts/lrm-1385-after-360-narrow-dark.png",
      fullPage: true,
    });
  });
});
