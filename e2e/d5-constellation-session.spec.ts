import { test, expect, type Page } from "@playwright/test";
import {
  deleteD5ConstellationSession,
  seedD5ConstellationSession,
  seedD5LargeConstellationSession,
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

async function openSession(page: Page, seeded: D5ConstellationSeed, width: number, height: number) {
  await page.setViewportSize({ width, height });
  await authenticate(page, seeded.api);
  await page.goto(`/${seeded.slug}/research/${seeded.sessionId}`, {
    waitUntil: "commit",
    timeout: 60_000,
  });
  await dismissOnboarding(page);
  await expect(page.getByTestId("star-graph-canvas")).toBeVisible({ timeout: 60_000 });
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

    await page.getByTestId("research-d5-lens-lineage").click();
    await expect(page.getByTestId("research-d5-active-lens")).toHaveText("lineage");

    const stable = page
      .getByRole("button", { name: new RegExp(seeded.nodeTitles.stable, "i") })
      .first();
    const goal = page.getByRole("button", { name: new RegExp(seeded.nodeTitles.goal, "i") }).first();
    await expect(stable).toHaveClass(/sg-lens-emphasis/);
    await expect(goal).toHaveClass(/sg-lens-dim/);
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
});
