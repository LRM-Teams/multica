#!/usr/bin/env node
/**
 * LRM-1185 evidence shots — real dev stack, real routes, 360px viewport.
 * Usage: node scripts/lrm1185-profile-close-shots.mjs <before|after>
 *
 * Captures, for both the agent and the member narrow-screen profile:
 *   1. the surface as it looks on arrival (is there a visible way out?)
 *   2. whether tapping the leading ← actually returns to the channel
 */
import { mkdirSync, readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const label = process.argv[2] ?? "shot";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1185");
mkdirSync(outDir, { recursive: true });

const base = process.env.SHOT_BASE_URL ?? "http://localhost:13316";
const ctx = JSON.parse(readFileSync("/tmp/lrm1185-ctx.json", "utf8"));

const browser = await chromium.launch();
const page = await browser.newPage({
  viewport: { width: 360, height: 780 },
  deviceScaleFactor: 2,
  isMobile: true,
  hasTouch: true,
  locale: "zh-CN",
});
page.setDefaultTimeout(90_000);
page.setDefaultNavigationTimeout(120_000);

await page.goto(`${base}/login`, { waitUntil: "domcontentloaded" });
await page.evaluate((t) => localStorage.setItem("multica_token", t), ctx.token);

const dismissOnboarding = async () => {
  for (const name of [/^Skip$/i, /^跳过$/]) {
    const skip = page.getByRole("button", { name }).first();
    if (await skip.isVisible().catch(() => false)) {
      await skip.click().catch(() => {});
      await page.waitForTimeout(1200);
    }
  }
};

const channelUrl = `${base}/${ctx.slug}/channels/${ctx.channelId}`;
const agentProfileUrl = `${base}/${ctx.slug}/profile/agent/${ctx.agentId}`;

async function shot(name) {
  await page.waitForTimeout(1500);
  await page.screenshot({ path: `${outDir}/${label}-${name}.png` });
}

// Dev-mode route compilation makes the first paint a skeleton; warm each route
// and then wait for real content before capturing.
async function open(url, readySelector) {
  for (let attempt = 0; attempt < 3; attempt += 1) {
    await page.goto(url, { waitUntil: "domcontentloaded" });
    await dismissOnboarding();
    await page
      .waitForLoadState("networkidle", { timeout: 60_000 })
      .catch(() => {});
    try {
      await page.locator(readySelector).first().waitFor({ timeout: 45_000 });
      return;
    } catch {
      console.log(`[${label}] retry ${url} (attempt ${attempt + 1})`);
    }
  }
  await page.screenshot({ path: `${outDir}/${label}-DEBUG.png` });
  throw new Error(`never became ready: ${url}`);
}

// --- Agent narrow-screen profile -------------------------------------------
await open(agentProfileUrl, '[data-testid="agent-profile-identity"]');
const agentClose = await page.getByTestId("side-panel-page-close").count();
const agentBack = await page.getByTestId("actor-profile-back").count();
console.log(`[${label}] agent page: floating close=${agentClose} strong back=${agentBack}`);
if (agentBack) {
  console.log(
    `[${label}] back box=${JSON.stringify(await page.getByTestId("actor-profile-back").boundingBox())}`,
  );
}
if (agentClose) {
  console.log(
    `[${label}] floating close box=${JSON.stringify(await page.getByTestId("side-panel-page-close").boundingBox())}`,
  );
}
await shot("agent-profile");

// --- Member narrow-screen profile, reached by tapping the avatar -----------
await open(channelUrl, '[data-testid="actor-profile-trigger"]');
const avatar = page.getByTestId("actor-profile-trigger").first();
await avatar.click();
await page.waitForTimeout(4000);
console.log(`[${label}] after avatar tap url=${page.url()}`);
await shot("member-profile-from-avatar");

// --- Does the exit actually work? ------------------------------------------
const backControl = page
  .getByTestId("actor-profile-back")
  .or(page.getByTestId("side-panel-page-close"))
  .first();
if (await backControl.count()) {
  const box = await backControl.boundingBox();
  console.log(`[${label}] exit control box=${JSON.stringify(box)}`);
  await backControl.click();
  await page.waitForTimeout(2500);
}
console.log(`[${label}] after exit tap url=${page.url()}`);
await shot("after-exit-tap");

await browser.close();
console.log(`[${label}] shots written to ${outDir}`);
