import { test, expect, type Page } from "@playwright/test";
import pg from "pg";
import { TestApiClient } from "./fixtures";

// LRM-789 — 调研主页 C 切片:进行中/已完成分组(无数字)+ 四态
// (加载等高骨架 / 失败面板+重试≠空态 / 空态 CTA 聚焦 composer / 分组列表)。

const API_BASE =
  process.env.NEXT_PUBLIC_API_URL ||
  `http://localhost:${process.env.PORT || "8080"}`;
const DATABASE_URL =
  process.env.DATABASE_URL ??
  "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

const E2E_EMAIL = "e2e-research@multica.ai";
const E2E_NAME = "E2E Research";
const WS_SLUG = "e2e-research-ws";

let api: TestApiClient;
let slug: string;
let workspaceId = "";
const seededSessionIds: string[] = [];

async function dbQuery(sql: string, params: unknown[] = []) {
  const client = new pg.Client(DATABASE_URL);
  await client.connect();
  try {
    return await client.query(sql, params);
  } finally {
    await client.end();
  }
}

async function authedFetch(path: string, init?: RequestInit) {
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${api.getToken()}`,
      "X-Workspace-Slug": slug,
      ...(init?.headers ?? {}),
    },
  });
  return res;
}

async function seedSession(goal: string, status: string, stage = "s1_plan", title?: string) {
  // Direct DB seed: POST /api/research/sessions requires a live agent runtime
  // to seed the fleet, which the local dev stack does not have.
  const fleet = await dbQuery("SELECT id FROM research_fleet WHERE workspace_id = $1", [workspaceId]);
  const user = await dbQuery('SELECT id FROM "user" WHERE email = $1', [E2E_EMAIL]);
  if (fleet.rows.length === 0 || user.rows.length === 0) {
    throw new Error("seed prerequisites missing: fleet or user row not found");
  }
  const res = await dbQuery(
    `INSERT INTO research_session (workspace_id, fleet_id, created_by, title, goal, status, current_stage)
     VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
    [workspaceId, fleet.rows[0].id, user.rows[0].id, title ?? goal, goal, status, stage],
  );
  const id = res.rows[0].id as string;
  seededSessionIds.push(id);
  return id;
}

async function dismissOnboarding(page: Page) {
  // Fresh-DB users get the onboarding follow-up dialog, which blocks clicks.
  const skip = page.getByRole("button", { name: /跳过|Skip/i });
  try {
    await skip.first().waitFor({ state: "visible", timeout: 4000 });
    await skip.first().click();
  } catch {
    // No dialog — nothing to dismiss.
  }
}

async function gotoResearch(page: Page) {
  await page.goto(`/${slug}/research`);
  // Page ready when the composer textarea is interactive.
  await expect(page.locator("textarea")).toBeVisible({ timeout: 60000 });
  await dismissOnboarding(page);
}

test.beforeAll(async () => {
  api = new TestApiClient();
  const data = await api.login(E2E_EMAIL, E2E_NAME);
  const ws = await api.ensureWorkspace("E2E Research Workspace", WS_SLUG);
  slug = ws.slug;
  workspaceId = ws.id;
  // Skip onboarding gates for the fresh DB user.
  await dbQuery('UPDATE "user" SET onboarded_at = now() WHERE email = $1', [E2E_EMAIL]);
  void data;
});

test.afterAll(async () => {
  if (workspaceId && seededSessionIds.length > 0) {
    await dbQuery("DELETE FROM research_session WHERE id = ANY($1::uuid[])", [seededSessionIds]);
  }
});

test.describe.serial("research list page — LRM-789 slice C", () => {
  test("empty state: icon + copy + CTA focuses composer", async ({ page }) => {
    // Clean slate: drop any leftover sessions from prior runs.
    await dbQuery("DELETE FROM research_session WHERE workspace_id = $1", [workspaceId]);

    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const empty = page.locator("div.border-dashed").last();
    await expect(empty).toBeVisible();
    const cta = empty.locator("button");
    await expect(cta).toBeVisible();
    await cta.click();
    await expect(page.locator("textarea")).toBeFocused();
    await page.screenshot({ path: "e2e/artifacts/lrm789-empty-desktop.png", fullPage: true });
  });

  test("loading: equal-height skeleton rows while sessions query is pending", async ({ page }) => {
    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());

    await page.route("**/api/research/sessions", async (route) => {
      await new Promise((r) => setTimeout(r, 1500));
      await route.fulfill({ json: { sessions: [] } });
    });
    await page.goto(`/${slug}/research`);
    const busy = page.locator('[aria-busy="true"]');
    await expect(busy).toBeVisible({ timeout: 60000 });
    const skeletons = busy.locator('[data-slot="skeleton"]');
    await expect(skeletons).toHaveCount(4);
  });

  test("error state: alert panel with retry, not the empty state", async ({ page }) => {
    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());

    let fail = true;
    await page.route("**/api/research/sessions", async (route) => {
      if (fail) {
        await route.fulfill({ status: 500, json: { error: "boom" } });
      } else {
        await route.fulfill({ json: { sessions: [] } });
      }
    });
    await page.goto(`/${slug}/research`);

    // Filter out Next's empty route announcer (also role="alert").
    const alert = page.getByRole("alert").filter({ has: page.getByRole("button") });
    await expect(alert).toBeVisible({ timeout: 60000 });
    await dismissOnboarding(page);
    await expect(page.locator("div.border-dashed").last()).not.toBeVisible();
    await page.screenshot({ path: "e2e/artifacts/lrm789-error-desktop.png", fullPage: true });

    fail = false;
    await alert.getByRole("button").click();
    await expect(alert).not.toBeVisible({ timeout: 15000 });
  });

  test("grouped list: 进行中/已完成 headers without counts (desktop)", async ({ page }) => {
    await dbQuery("DELETE FROM research_session WHERE workspace_id = $1", [workspaceId]);
    await seedSession("Alpha market map", "running");
    await seedSession("Beta competitor scan", "awaiting_user_confirm");
    await seedSession("Gamma done report", "completed");

    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const headers = page.locator("section h2");
    await expect(headers).toHaveCount(2, { timeout: 15000 });
    const inProgress = headers.first();
    const completed = headers.last();
    // No counts in headers.
    await expect(inProgress).not.toContainText(/\d/);
    await expect(completed).not.toContainText(/\d/);

    // Filter out the notification toaster, which is also a <section>.
    const sections = page.locator("section").filter({ has: page.locator("h2") });
    await expect(sections.first()).toContainText("Alpha market map");
    await expect(sections.first()).toContainText("Beta competitor scan");
    await expect(sections.first()).not.toContainText("Gamma done report");
    await expect(sections.last()).toContainText("Gamma done report");
    await expect(sections.last()).not.toContainText("Alpha market map");

    await page.screenshot({ path: "e2e/artifacts/lrm789-grouped-desktop.png", fullPage: true });
  });

  test("grouped list on narrow viewport (390px)", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const headers = page.locator("section h2");
    await expect(headers).toHaveCount(2, { timeout: 15000 });
    const groupSections = page.locator("section").filter({ has: page.locator("h2") });
    await expect(groupSections.first()).toContainText("Alpha market map");
    await page.screenshot({ path: "e2e/artifacts/lrm789-grouped-narrow.png", fullPage: true });
  });
});

test.describe.serial("research list rows — LRM-788 slice B", () => {
  // Row composition: semantic status dot (running pulses) + truncated
  // title/goal + stage chip + fleet avatar stack + relative time + hover
  // chevron; the whole row is a link.

  test.beforeAll(async () => {
    // An online runtime lets the list handler seed the workspace fleet
    // (罗纳尔多 et al.), so fleet_preview / the avatar stack have members.
    await dbQuery(
      `INSERT INTO agent_runtime (
         workspace_id, daemon_id, name, runtime_mode, provider, status,
         visibility, device_info, metadata, last_seen_at
       )
       VALUES ($1, NULL, $2, 'cloud', 'e2e_research_runtime', 'online',
               'public', 'E2E research runtime', '{}'::jsonb, now())`,
      [workspaceId, `e2e research runtime ${Date.now()}`],
    );
    await dbQuery("DELETE FROM research_session WHERE workspace_id = $1", [workspaceId]);
    // First hit (re)seeds the fleet members before rows are asserted.
    const res = await authedFetch("/api/research/sessions");
    if (!res.ok) throw new Error(`fleet warm-up failed: ${res.status}`);
  });

  test("row shows status dot, stage chip, avatar stack, relative time (desktop)", async ({ page }) => {
    await dbQuery("DELETE FROM research_session WHERE workspace_id = $1", [workspaceId]);
    await seedSession("Map the alpha market", "running", "s2_sources", "Alpha market map");
    await seedSession("Delta delivered report", "completed", "s4_delivery");

    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const runningRow = page.locator("div.group", { hasText: "Alpha market map" }).first();
    await expect(runningRow).toBeVisible({ timeout: 15000 });

    // Semantic status dot: brand + pulse for running.
    const dot = runningRow.locator("span.rounded-full.size-2").first();
    await expect(dot).toHaveClass(/animate-pulse/);

    // Stage chip (locale-safe pattern) and relative time.
    await expect(runningRow).toContainText(/S2\s*·/);
    await expect(runningRow).toContainText(/刚刚|just now|分钟前|minutes? ago/i);

    // Fleet avatar stack renders at least one head.
    await expect(runningRow.locator("span.rounded-full.ring-2").first()).toBeVisible();

    const completedRow = page.locator("div.group", { hasText: "Delta delivered report" }).first();
    const doneDot = completedRow.locator("span.rounded-full.size-2").first();
    await expect(doneDot).not.toHaveClass(/animate-pulse/);
    await expect(completedRow).toContainText(/S4\s*·/);

    await page.screenshot({ path: "e2e/artifacts/lrm788-rows-desktop.png", fullPage: true });
  });

  test("hover reveals chevron; whole-row click navigates to the session", async ({ page }) => {
    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const row = page.locator("div.group", { hasText: "Alpha market map" }).first();
    await expect(row).toBeVisible({ timeout: 15000 });

    const chevron = row.locator("svg.opacity-0").first();
    await expect(chevron).toHaveCSS("opacity", "0");
    await row.hover();
    const chevronAfter = row.locator("svg").last();
    await expect(chevronAfter).toHaveCSS("opacity", "1");

    await row.locator("a").first().click();
    await expect(page).toHaveURL(/\/research\/[0-9a-f-]{36}/, { timeout: 15000 });
  });

  test("rows on narrow viewport (390px)", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const row = page.locator("div.group", { hasText: "Alpha market map" }).first();
    await expect(row).toBeVisible({ timeout: 15000 });
    await expect(row).toContainText(/S2\s*·/);
    await page.screenshot({ path: "e2e/artifacts/lrm788-rows-narrow.png", fullPage: true });
  });

  test("rows in dark mode (semantic tokens)", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "dark" });
    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const row = page.locator("div.group", { hasText: "Alpha market map" }).first();
    await expect(row).toBeVisible({ timeout: 15000 });
    await expect(page.locator("html")).toHaveClass(/dark/);
    await page.screenshot({ path: "e2e/artifacts/lrm788-rows-dark.png", fullPage: true });
  });
});
