import { test, expect, type Page } from "@playwright/test";
import {
  deleteD5ConstellationSession,
  seedD5ConstellationSession,
  seedD5FormingConstellationSession,
  seedD5LargeConstellationSession,
  seedD5PaginatedConstellationSession,
  seedD5SparseConstellationSession,
  type D5ConstellationSeed,
} from "./fixtures/d5-constellation-seed";

const runIntegration = process.env.D5_CONSTELLATION_E2E === "1";

const VIEWPORT_MATRIX: ReadonlyArray<[number, number]> = [
  [360, 800],
  [768, 900],
  [1100, 800],
  [1440, 900],
  [1920, 1080],
];

const ZOOM_TARGETS = [25, 78, 100, 200] as const;

async function authenticate(page: Page, api: D5ConstellationSeed["api"]) {
  await page.goto("/login");
  await page.evaluate((token) => localStorage.setItem("multica_token", token), api.getToken());
}

async function dismissOnboarding(page: Page) {
  const skip = page.getByRole("button", { name: /跳过|Skip/i });
  try {
    await skip.first().waitFor({ state: "visible", timeout: 4000 });
    await skip.first().click();
  } catch {
    // No dialog.
  }
}

async function openSession(
  page: Page,
  seeded: D5ConstellationSeed,
  width: number,
  height: number,
  options: { expectCanvas?: boolean } = {},
) {
  const expectCanvas = options.expectCanvas ?? true;
  await page.setViewportSize({ width, height });
  await authenticate(page, seeded.api);
  await page.goto(`/${seeded.slug}/research/${seeded.sessionId}`, {
    waitUntil: "commit",
    timeout: 60_000,
  });
  await dismissOnboarding(page);
  if (expectCanvas) {
    await expect(page.getByTestId("star-graph-canvas")).toBeVisible({ timeout: 60_000 });
  }
}

async function readZoomPercent(page: Page): Promise<number> {
  const text = await page.getByTestId("star-graph-zoom-controls").innerText();
  const match = text.match(/(\d+)%/);
  return match ? Number(match[1]) : 100;
}

async function setZoomApprox(page: Page, targetPct: number) {
  const controls = page.getByTestId("star-graph-zoom-controls");
  await controls.getByRole("button", { name: /适应内容|Fit/i }).click();
  await expect.poll(() => readZoomPercent(page)).toBeGreaterThan(0);

  if (targetPct === 100) return;

  const zoomOut = controls.getByRole("button", { name: /缩小|Zoom out/i });
  const zoomIn = controls.getByRole("button", { name: /放大|Zoom in/i });

  for (let attempt = 0; attempt < 40; attempt += 1) {
    const current = await readZoomPercent(page);
    if (Math.abs(current - targetPct) <= 8) return;
    if (current > targetPct) await zoomOut.click();
    else await zoomIn.click();
  }
}

test.describe.serial("D5 constellation session canvas gate", () => {
  test.skip(!runIntegration, "Set D5_CONSTELLATION_E2E=1 with backend + frontend running");
  test.setTimeout(180_000);

  let seeded: D5ConstellationSeed;

  test.beforeAll(async () => {
    seeded = await seedD5ConstellationSession("D5 Constellation E2E");
  });

  test.afterAll(async () => {
    await deleteD5ConstellationSession(seeded?.sessionId);
  });

  test("desktop: renders D5 chrome, star graph, map key and zoom controls", async ({ page }) => {
    await openSession(page, seeded, 1440, 900);

    await expect(page.getByTestId("research-d5-chrome")).toBeVisible();
    await expect(page.getByTestId("research-d5-lens-relations")).toBeVisible();
    await expect(page.getByTestId("star-graph-entities")).toBeVisible();
    await expect(page.getByTestId("star-graph-edges")).toBeVisible();
    await expect(page.getByTestId("star-graph-zoom-controls")).toBeVisible();
    await expect(page.getByTestId("star-graph-summary")).toBeVisible();

    await expect(page.getByRole("button", { name: new RegExp(seeded.nodeTitles.goal, "i") })).toBeVisible();
    await expect(page.getByRole("button", { name: new RegExp(seeded.nodeTitles.stable, "i") })).toBeVisible();
    await expect(page.getByRole("button", { name: new RegExp(seeded.nodeTitles.probe, "i") })).toBeVisible();

    await expect(page.locator(".react-flow")).toHaveCount(0);
    await expect(page.locator("[data-aggregate-tier]")).toHaveCount(0);

    await page.screenshot({
      path: "e2e/artifacts/d5-constellation-desktop-1440.png",
      fullPage: true,
    });
  });

  test("desktop: selecting a node opens the detail rail", async ({ page }) => {
    await openSession(page, seeded, 1440, 900);
    const target = page.getByRole("button", { name: new RegExp(seeded.nodeTitles.stable, "i") }).first();
    await target.click();
    await expect(target).toBeFocused();
    await expect(page.getByTestId("research-d5-rail")).toBeVisible();
  });

  test("desktop: keyboard zoom controls adjust the percentage label", async ({ page }) => {
    await openSession(page, seeded, 1440, 900);
    const canvas = page.getByTestId("star-graph-canvas");
    await canvas.focus();
    const before = await page.getByTestId("star-graph-zoom-controls").innerText();
    await page.keyboard.press("+");
    await expect
      .poll(async () => page.getByTestId("star-graph-zoom-controls").innerText())
      .not.toBe(before);
  });

  test("desktop: rail toggle hides and restores the detail rail", async ({ page }) => {
    await openSession(page, seeded, 1440, 900);
    const workspace = page.getByTestId("research-constellation-workspace");
    await expect(workspace).toHaveAttribute("data-d5-rail-open", "true");

    await page.getByTestId("research-d5-rail-toggle").click();
    await expect(workspace).toHaveAttribute("data-d5-rail-open", "false");
    await expect(page.getByTestId("research-d5-rail")).toHaveCount(0);

    await page.getByTestId("research-d5-rail-toggle").click();
    await expect(workspace).toHaveAttribute("data-d5-rail-open", "true");
    await expect(page.getByTestId("research-d5-rail")).toBeVisible();
  });

  test("desktop: lens tabs switch active lens without leaving the session", async ({ page }) => {
    await openSession(page, seeded, 1440, 900);
    await expect(page.getByTestId("research-d5-active-lens")).toHaveText("relations");

    await page.getByTestId("research-d5-lens-confidence").click();
    await expect(page.getByTestId("research-d5-active-lens")).toHaveText("confidence");

    await page.getByTestId("research-d5-lens-agent").click();
    await expect(page.getByTestId("research-d5-active-lens")).toHaveText("agent");

    await page.getByTestId("research-d5-lens-lineage").click();
    await expect(page.getByTestId("research-d5-active-lens")).toHaveText("lineage");

    const stable = page
      .getByRole("button", { name: new RegExp(seeded.nodeTitles.stable, "i") })
      .first();
    const goal = page.getByRole("button", { name: new RegExp(seeded.nodeTitles.goal, "i") }).first();
    await expect(stable).toHaveClass(/sg-lens-emphasis/);
    await expect(goal).toHaveClass(/sg-lens-dim/);
  });

  test("desktop: goal card opens the version panel with the session goal", async ({ page }) => {
    await openSession(page, seeded, 1440, 900);
    await page.getByTestId("research-session-goal-card").click();
    await expect(page.getByTestId("research-session-goal-popover")).toBeVisible();
    await expect(page.getByTestId("research-session-goal-full")).toContainText(
      "Verify the D5 star-map session canvas",
    );
    await page.screenshot({
      path: "e2e/artifacts/d5-constellation-goal-panel-1440.png",
      fullPage: true,
    });
  });

  test("desktop: L node opens the node report modal with merge lineage", async ({ page }) => {
    await openSession(page, seeded, 1440, 900);
    await page
      .getByRole("button", { name: new RegExp(seeded.nodeTitles.stable, "i") })
      .first()
      .click();
    await expect(page.getByTestId("research-node-report-modal")).toBeVisible();
    if (seeded.nodeTitles.prior) {
      await expect(
        page.locator(`[data-testid^="research-node-report-lineage-"]`).first(),
      ).toBeVisible();
    }
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("research-node-report-modal")).toHaveCount(0);
  });

  test("desktop: Work node opens Agent settings in the context rail", async ({ page }) => {
    test.skip(!seeded.fleetAgentId, "Fleet agent id unavailable in seed");
    await openSession(page, seeded, 1440, 900);
    await page
      .getByRole("button", { name: new RegExp(seeded.nodeTitles.probe, "i") })
      .first()
      .click();
    await expect(page.getByTestId("research-d5-rail")).toHaveAttribute(
      "data-rail-mode",
      "detail",
    );
    await page.getByRole("tab", { name: /智能体设置|Agent settings/i }).click();
    await expect(page.getByTestId("research-d5-rail")).toHaveAttribute(
      "data-rail-mode",
      "agent",
    );
    await expect(page.getByTestId("agent-profile-identity")).toBeVisible();
  });

  test("desktop: rail switches between chat and detail panels", async ({ page }) => {
    await openSession(page, seeded, 1440, 900);
    await page
      .getByRole("button", { name: new RegExp(seeded.nodeTitles.stable, "i") })
      .first()
      .click();
    await expect(page.getByTestId("research-d5-rail")).toHaveAttribute("data-rail-mode", "detail");

    await page.getByRole("button", { name: /聊天|Chat/i }).click();
    await expect(page.getByTestId("research-d5-rail")).toHaveAttribute("data-rail-mode", "chat");

    await page.getByRole("button", { name: /节点详情|Node detail/i }).click();
    await expect(page.getByTestId("research-d5-rail")).toHaveAttribute("data-rail-mode", "detail");
  });

  test("desktop: canvas filter round narrows visible nodes", async ({ page }) => {
    await openSession(page, seeded, 1440, 900);
    await page.getByTestId("research-d5-filter-trigger").click();
    await expect(page.getByTestId("research-d5-filter-popover")).toBeVisible();
    await page.getByTestId("research-d5-filter-round").selectOption("1");
    await page.getByTestId("research-d5-lens-lineage").click();
    const prior = seeded.nodeTitles.prior
      ? page.getByRole("button", { name: new RegExp(seeded.nodeTitles.prior, "i") }).first()
      : null;
    const stable = page
      .getByRole("button", { name: new RegExp(seeded.nodeTitles.stable, "i") })
      .first();
    if (prior) await expect(prior).toHaveClass(/sg-lens-emphasis/);
    await expect(stable).toHaveClass(/sg-lens-dim/);
  });

  test("desktop: keyboard Home focuses the goal node", async ({ page }) => {
    await openSession(page, seeded, 1440, 900);
    await page
      .getByRole("button", { name: new RegExp(seeded.nodeTitles.stable, "i") })
      .first()
      .click();
    await page.getByTestId("star-graph-canvas").focus();
    await page.keyboard.press("Home");
    await expect(
      page.getByRole("button", { name: new RegExp(seeded.nodeTitles.goal, "i") }).first(),
    ).toBeFocused();
  });

  test("desktop: reduced motion preference keeps the canvas usable", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await openSession(page, seeded, 1440, 900);
    await expect(page.getByTestId("star-graph-canvas")).toBeVisible();
    await expect(page.getByTestId("star-graph-entities")).toBeVisible();
    await page.screenshot({
      path: "e2e/artifacts/d5-constellation-reduced-motion-1440.png",
      fullPage: true,
    });
  });

  for (const [width, height] of VIEWPORT_MATRIX) {
    test(`visual matrix: ${width}x${height} session screenshot`, async ({ page }) => {
      await openSession(page, seeded, width, height);
      await expect(page.getByTestId("research-d5-chrome")).toBeVisible();
      await expect(page.getByTestId("star-graph-canvas")).toBeVisible();
      await page.screenshot({
        path: `e2e/artifacts/d5-constellation-${width}x${height}.png`,
        fullPage: true,
      });
    });
  }

  for (const targetZoom of ZOOM_TARGETS) {
    test(`visual matrix: zoom ~${targetZoom}% at 1440x900`, async ({ page }) => {
      await openSession(page, seeded, 1440, 900);
      await setZoomApprox(page, targetZoom);
      await expect
        .poll(() => readZoomPercent(page))
        .toBeGreaterThanOrEqual(Math.max(25, targetZoom - 10));
      await page.screenshot({
        path: `e2e/artifacts/d5-constellation-zoom-${targetZoom}.png`,
        fullPage: true,
      });
    });
  }

  test("mobile: star graph fits without horizontal overflow", async ({ page }) => {
    await openSession(page, seeded, 390, 844);
    await expect(page.getByTestId("research-d5-rail")).toBeAttached();
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
      .toBe(true);
    await expect(page.getByRole("button", { name: new RegExp(seeded.nodeTitles.goal, "i") })).toBeVisible();
    await page.screenshot({
      path: "e2e/artifacts/d5-constellation-mobile-390.png",
      fullPage: true,
    });
  });

  test("mobile: Work node exposes Agent settings in the context sheet", async ({ page }) => {
    test.skip(!seeded.fleetAgentId, "Fleet agent id unavailable in seed");
    await openSession(page, seeded, 390, 844);
    await page
      .getByRole("button", { name: new RegExp(seeded.nodeTitles.probe, "i") })
      .first()
      .click();
    await expect(page.getByTestId("research-d5-mobile-rail")).toBeVisible();
    await page.getByRole("tab", { name: /智能体设置|Agent settings/i }).click();
    await expect(page.getByTestId("research-d5-rail")).toHaveAttribute(
      "data-rail-mode",
      "agent",
    );
    await expect(page.getByTestId("agent-profile-identity")).toBeVisible();
  });

  test("mobile: L node opens report modal over the canvas", async ({ page }) => {
    await openSession(page, seeded, 390, 844);
    await page
      .getByRole("button", { name: new RegExp(seeded.nodeTitles.stable, "i") })
      .first()
      .click();
    await expect(page.getByTestId("research-node-report-modal")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("research-node-report-modal")).toHaveCount(0);
  });
});

test.describe.serial("D5 large graph budget gate", () => {
  test.skip(!runIntegration, "Set D5_CONSTELLATION_E2E=1 with backend + frontend running");
  test.setTimeout(180_000);

  let largeSeed: Awaited<ReturnType<typeof seedD5LargeConstellationSession>>;

  test.beforeAll(async () => {
    largeSeed = await seedD5LargeConstellationSession("D5 Large Constellation E2E");
  });

  test.afterAll(async () => {
    await deleteD5ConstellationSession(largeSeed?.sessionId);
  });

  test("low zoom collapses clusters and surfaces hidden-count badges", async ({ page }) => {
    await openSession(page, largeSeed, 1440, 900);
    await setZoomApprox(page, 25);
    await expect(page.getByTestId("star-graph-budget-note")).toBeVisible();
    await expect(
      page.locator('[data-testid^="star-graph-cluster-hidden-"]').first(),
    ).toBeVisible();
    await page.screenshot({
      path: "e2e/artifacts/d5-constellation-large-low-zoom.png",
      fullPage: true,
    });
  });

  test("keeps mounted entity controls within the desktop DOM budget at fit zoom", async ({ page }) => {
    await openSession(page, largeSeed, 1440, 900);
    await setZoomApprox(page, 100);
    const mounted = await page.getByTestId("star-graph-entities").locator("button").count();
    expect(mounted).toBeLessThanOrEqual(220);
  });
});

test.describe.serial("D5 sparse metrics gate", () => {
  test.skip(!runIntegration, "Set D5_CONSTELLATION_E2E=1 with backend + frontend running");
  test.setTimeout(180_000);

  let sparseSeed: D5ConstellationSeed;

  test.beforeAll(async () => {
    sparseSeed = await seedD5SparseConstellationSession("D5 Sparse Metrics E2E");
  });

  test.afterAll(async () => {
    await deleteD5ConstellationSession(sparseSeed?.sessionId);
  });

  test("does not render fabricated zero metric placeholders", async ({ page }) => {
    await openSession(page, sparseSeed, 1440, 900);
    const finding = page
      .getByRole("button", { name: new RegExp(sparseSeed.nodeTitles.stable, "i") })
      .first();
    await expect(finding).toBeVisible();
    await expect(finding).not.toContainText(/^0$/);
    await expect(finding).not.toContainText(/0%/);
    await page.screenshot({
      path: "e2e/artifacts/d5-constellation-sparse-metrics-1440.png",
      fullPage: true,
    });
  });
});

test.describe.serial("D5 forming session gate", () => {
  test.skip(!runIntegration, "Set D5_CONSTELLATION_E2E=1 with backend + frontend running");
  test.setTimeout(180_000);

  let formingSeed: D5ConstellationSeed;

  test.beforeAll(async () => {
    formingSeed = await seedD5FormingConstellationSession("D5 Forming E2E");
  });

  test.afterAll(async () => {
    await deleteD5ConstellationSession(formingSeed?.sessionId);
  });

  test("shows the forming scaffold before business edges exist", async ({ page }) => {
    await openSession(page, formingSeed, 1440, 900, { expectCanvas: false });
    await expect(page.getByTestId("research-session-canvas-forming")).toBeVisible({
      timeout: 60_000,
    });
    await expect(page.getByTestId("star-graph-canvas")).toHaveCount(0);
    await page.screenshot({
      path: "e2e/artifacts/d5-constellation-forming-1440.png",
      fullPage: true,
    });
  });
});

test.describe.serial("D5 paginated typed-graph gate", () => {
  test.skip(!runIntegration, "Set D5_CONSTELLATION_E2E=1 with backend + frontend running");
  test.setTimeout(300_000);

  let paginatedSeed: Awaited<ReturnType<typeof seedD5PaginatedConstellationSession>>;

  test.beforeAll(async () => {
    paginatedSeed = await seedD5PaginatedConstellationSession("D5 Paginated E2E");
  });

  test.afterAll(async () => {
    await deleteD5ConstellationSession(paginatedSeed?.sessionId);
  });

  test("first page shows load-more while DOM stays within budget", async ({ page }) => {
    await openSession(page, paginatedSeed, 1440, 900);
    const loadMore = page.getByTestId("star-graph-load-more");
    await expect(loadMore).toBeVisible({ timeout: 60_000 });
    await expect(loadMore).toContainText(/21|加载更多|Load more/i);
    const mounted = await page.getByTestId("star-graph-entities").locator("button").count();
    expect(mounted).toBeLessThanOrEqual(220);
    await expect(page.getByTestId("star-graph-budget-note")).toBeVisible();
  });

  test("load-more fetches the remaining canonical page", async ({ page }) => {
    await openSession(page, paginatedSeed, 1440, 900);
    const loadMore = page.getByTestId("star-graph-load-more");
    await expect(loadMore).toBeVisible({ timeout: 60_000 });
    await loadMore.click();
    await expect(loadMore).toHaveCount(0, { timeout: 60_000 });
    await page.screenshot({
      path: "e2e/artifacts/d5-constellation-paginated-loaded-1440.png",
      fullPage: true,
    });
  });
});
