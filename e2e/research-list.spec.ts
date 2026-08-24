import { test, expect, type Page } from "@playwright/test";
import pg from "pg";
import { TestApiClient } from "./fixtures";

// LRM-789 — 调研主页 C 切片:进行中/已完成分组 + 四态
// (加载等高骨架 / 失败面板+重试≠空态 / 空态 CTA 聚焦 composer / 分组列表)。

const API_BASE =
  process.env.NEXT_PUBLIC_API_URL ||
  `http://localhost:${process.env.PORT || "8080"}`;
const DATABASE_URL =
  process.env.DATABASE_URL ??
  "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

const E2E_EMAIL = `e2e-research-${Date.now()}@multica.ai`;
const E2E_NAME = "E2E Research";
const WS_SLUG = `e2e-research-ws-${Date.now()}`;

let api: TestApiClient;
let slug: string;
let workspaceId = "";
let rowSessionId = "";

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
  const create = await authedFetch("/api/research/sessions", {
    method: "POST",
    body: JSON.stringify({ goal, title: title ?? goal, depth_tier: "shallow" }),
  });
  if (!create.ok) {
    throw new Error(`create research session failed: ${create.status} ${await create.text()}`);
  }
  const created = (await create.json()) as { session: { id: string } };
  const id = created.session.id;
  await dbQuery(
    `UPDATE research_session
     SET title = $2, goal = $3, status = $4, current_stage = $5, updated_at = now()
     WHERE id = $1 AND workspace_id = $6`,
    [id, title ?? goal, goal, status, stage, workspaceId],
  );
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
  const token = api.getToken();
  if (!token) throw new Error("research E2E client is not authenticated");
  await page.addInitScript((value) => {
    localStorage.setItem("multica_token", value);
  }, token);
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
  await api.ensureWorkspaceReady(ws);
  await dbQuery(
    `INSERT INTO agent_runtime (
       workspace_id, daemon_id, name, runtime_mode, provider, status,
       visibility, device_info, metadata, last_seen_at
     )
     VALUES ($1, NULL, $2, 'cloud', 'e2e_research_runtime', 'online',
             'public', 'E2E research runtime', '{}'::jsonb, now())`,
    [workspaceId, `e2e research runtime ${Date.now()}`],
  );
  const warm = await authedFetch("/api/research/sessions");
  if (!warm.ok) throw new Error(`fleet warm-up failed: ${warm.status}`);
  void data;
});

test.describe.serial("research list page — LRM-789 slice C", () => {
  test("empty state: icon + copy + CTA focuses composer", async ({ page }) => {
    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const empty = page.getByRole("region", { name: "No research yet" });
    await expect(empty).toBeVisible();
    const cta = empty.getByRole("button", { name: "Start your first research" });
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
    const busy = page.getByTestId("research-session-list-skeleton");
    await expect(busy).toBeVisible({ timeout: 60000 });
    await expect(busy.getByTestId("research-session-row-skeleton")).toHaveCount(4);
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
    await expect(page.getByRole("region", { name: "No research yet" })).not.toBeVisible();
    await page.screenshot({ path: "e2e/artifacts/lrm789-error-desktop.png", fullPage: true });

    fail = false;
    await alert.getByRole("button").click();
    await expect(alert).not.toBeVisible({ timeout: 15000 });
  });

  test("grouped list: 进行中/已完成 headers with counts (desktop)", async ({ page }) => {
    await seedSession("Alpha market map", "running");
    await seedSession("Beta competitor scan", "awaiting_user_confirm");
    await seedSession("Gamma done report", "completed");

    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const inProgressGroup = page.getByTestId("research-session-group-in-progress");
    const completedGroup = page.getByTestId("research-session-group-completed");
    const inProgress = inProgressGroup.getByRole("heading");
    const completed = completedGroup.getByRole("heading");
    await expect(inProgressGroup).toBeVisible({ timeout: 15000 });
    await expect(completedGroup).toBeVisible();
    await expect(inProgress).toContainText(/In progress\s*2/);
    await expect(completed).toContainText(/Completed\s*1/);

    await expect(inProgressGroup).toContainText("Alpha market map");
    await expect(inProgressGroup).toContainText("Beta competitor scan");
    await expect(inProgressGroup).not.toContainText("Gamma done report");
    await expect(completedGroup).toContainText("Gamma done report");
    await expect(completedGroup).not.toContainText("Alpha market map");

    await page.screenshot({ path: "e2e/artifacts/lrm789-grouped-desktop.png", fullPage: true });
  });

  test("grouped list on narrow viewport (390px)", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const inProgressGroup = page.getByTestId("research-session-group-in-progress");
    const completedGroup = page.getByTestId("research-session-group-completed");
    await expect(inProgressGroup).toBeVisible({ timeout: 15000 });
    await expect(completedGroup).toBeVisible();
    await expect(inProgressGroup).toContainText("Alpha market map");
    await page.screenshot({ path: "e2e/artifacts/lrm789-grouped-narrow.png", fullPage: true });
  });
});

test.describe.serial("research list rows — LRM-788 slice B", () => {
  // Row composition: semantic status dot (running pulses) + truncated
  // title/goal + stage chip + fleet avatar stack + relative time + hover
  // chevron; the whole row is a link.

  test("row shows status dot, stage chip, avatar stack, and time (desktop)", async ({ page }) => {
    rowSessionId = await seedSession(
      "Map the alpha market",
      "running",
      "s2_sources",
      "Alpha market map",
    );
    await seedSession("Delta delivered report", "completed", "s4_delivery");

    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const runningRow = page.locator(
      `[data-testid="research-session-row"][data-session-id="${rowSessionId}"]`,
    );
    await expect(runningRow).toBeVisible({ timeout: 15000 });

    // Semantic status dot: brand + pulse for running.
    const dot = runningRow.locator("span.rounded-full.size-2").first();
    await expect(dot).toHaveClass(/animate-pulse/);

    // Stage chip (locale-safe pattern) and local time.
    await expect(runningRow).toContainText(/S[1-4]\s*·/);
    await expect(runningRow).toContainText(/\d{1,2}:\d{2}/);

    // Fleet avatar stack renders at least one head.
    await expect(runningRow.locator("span.rounded-full.ring-2").first()).toBeVisible();

    const completedRow = page
      .getByTestId("research-session-row")
      .filter({ hasText: "Delta delivered report" });
    const doneDot = completedRow.locator("span.rounded-full.size-2").first();
    await expect(doneDot).not.toHaveClass(/animate-pulse/);
    await expect(completedRow).toContainText(/S[1-4]\s*·/);

    await page.screenshot({ path: "e2e/artifacts/lrm788-rows-desktop.png", fullPage: true });
  });

  test("hover reveals row actions; title click navigates to the session", async ({ page }) => {
    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const row = page.locator(
      `[data-testid="research-session-row"][data-session-id="${rowSessionId}"]`,
    );
    await expect(row).toBeVisible({ timeout: 15000 });

    const actions = row.getByRole("button", { name: "Research actions" });
    const actionsShell = actions.locator("..");
    await expect(actionsShell).toHaveCSS("opacity", "0");
    await row.hover();
    await expect(actionsShell).toHaveCSS("opacity", "1");

    await row.getByRole("link").first().click();
    await expect(page).toHaveURL(/\/research\/[0-9a-f-]{36}/, { timeout: 15000 });
  });

  test("rows on narrow viewport (390px)", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const row = page.locator(
      `[data-testid="research-session-row"][data-session-id="${rowSessionId}"]`,
    );
    await expect(row).toBeVisible({ timeout: 15000 });
    await expect(row).toContainText(/S[1-4]\s*·/);
    await page.screenshot({ path: "e2e/artifacts/lrm788-rows-narrow.png", fullPage: true });
  });

  test("rows in dark mode (semantic tokens)", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "dark" });
    await page.goto("/login");
    await page.evaluate((t) => localStorage.setItem("multica_token", t), api.getToken());
    await gotoResearch(page);

    const row = page.locator(
      `[data-testid="research-session-row"][data-session-id="${rowSessionId}"]`,
    );
    await expect(row).toBeVisible({ timeout: 15000 });
    await expect(page.locator("html")).toHaveClass(/dark/);
    await page.screenshot({ path: "e2e/artifacts/lrm788-rows-dark.png", fullPage: true });
  });
});
