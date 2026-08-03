#!/usr/bin/env node
/**
 * LRM-1153 evidence shots — real app, real API, real message row.
 * Usage: node scripts/lrm1153-bare-channel-ref-shots.mjs <label> [viewportWidth]
 * Requires the worktree dev stack and /tmp/lrm1153-ctx.json from the seed script.
 */
import { mkdirSync, readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const label = process.argv[2] ?? "shot";
const width = Number(process.argv[3] ?? 1440);
const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1153");
mkdirSync(outDir, { recursive: true });

const base = process.env.SHOT_BASE_URL ?? "http://localhost:13656";
const ctx = JSON.parse(readFileSync("/tmp/lrm1153-ctx.json", "utf8"));

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width, height: 900 }, locale: "zh-CN" });
page.setDefaultTimeout(120_000);
page.setDefaultNavigationTimeout(180_000);

// Authenticate the browser session the same way e2e/helpers.ts does — inject
// the API-issued token rather than driving the OTP boxes, which is flaky under
// a headless run and is not what this evidence is about.
await page.goto(`${base}/login`, { waitUntil: "domcontentloaded" });
await page.evaluate((t) => localStorage.setItem("multica_token", t), ctx.token);

await page.goto(`${base}/${ctx.slug}/channels/${ctx.patrol}`, { waitUntil: "domcontentloaded" });
for (const name of [/^Skip$/i, /^跳过$/]) {
  const skip = page.getByRole("button", { name }).first();
  if (await skip.isVisible().catch(() => false)) {
    await skip.click().catch(() => {});
    await page.waitForTimeout(1500);
  }
}
try {
  await page.waitForSelector("text=巡检增量", { timeout: 60000 });
} catch (err) {
  await page.screenshot({ path: `${outDir}/${label}-DEBUG.png` });
  console.error(`[${label}] message row never appeared at ${page.url()}`);
  console.error((await page.locator("body").innerText().catch(() => "")).slice(0, 1200));
  throw err;
}
await page.waitForTimeout(2500);

const chips = await page.getByTestId("channel-chip").allInnerTexts();
console.log(`[${label}] channel chips rendered: ${JSON.stringify(chips)}`);
const bodyText = await page.locator("body").innerText();
console.log(`[${label}] raw "#pr-frontend" still in text: ${bodyText.includes("#pr-frontend")}`);

await page.screenshot({ path: `${outDir}/${label}-${width}-full.png` });
// Clip the whole conversation column so both timeline rows (the one sent by
// the pre-fix backend and the one sent by the fixed backend) are side by side
// in a single frame.
const rows = page.locator("text=巡检增量");
const first = await rows.first().boundingBox();
const last = await rows.last().boundingBox();
if (first && last) {
  const x = Math.max(0, first.x - 70);
  const y = Math.max(0, first.y - 70);
  await page.screenshot({
    path: `${outDir}/${label}-${width}-rows.png`,
    clip: {
      x,
      y,
      width: Math.min(width - x, Math.max(first.width, last.width) + 320),
      height: Math.min(900 - y, last.y + last.height + 40 - y),
    },
  });
}
await browser.close();
console.log(`[${label}] shots written to ${outDir}`);
