import { test, expect, type Page } from "@playwright/test";
import {
  deleteD5ConstellationSession,
  seedD5ConstellationSession,
  type D5ConstellationSeed,
} from "./fixtures/d5-constellation-seed";

const runIntegration = process.env.D5_CONSTELLATION_E2E === "1";

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

test.describe.serial("D5 constellation session canvas gate", () => {
  test.skip(!runIntegration, "Set D5_CONSTELLATION_E2E=1 with backend + frontend running");
  test.setTimeout(120_000);

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
