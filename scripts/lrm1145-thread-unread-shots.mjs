#!/usr/bin/env node
/**
 * LRM-1145 evidence shots — real app, real API, real ThreadReplyPreview.
 * Usage: node scripts/lrm1145-thread-unread-shots.mjs <label>
 * Requires the worktree dev stack (backend + web) and the seeded fixture from
 * /tmp/lrm1145-ctx.json.
 */
import { mkdirSync, readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const label = process.argv[2] ?? "shot";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1145");
mkdirSync(outDir, { recursive: true });

const base = process.env.SHOT_BASE_URL ?? "http://localhost:13645";
const ctx = JSON.parse(readFileSync("/tmp/lrm1145-ctx.json", "utf8"));

const browser = await chromium.launch();
const page = await browser.newPage({
  viewport: { width: 1440, height: 900 },
  locale: "zh-CN",
});
page.setDefaultTimeout(120_000);
page.setDefaultNavigationTimeout(180_000);

await page.goto(`${base}/login`, { waitUntil: "domcontentloaded" });
await page.waitForTimeout(3000);
await page.locator("input").first().fill("frank@lrm1145.test");
await page.keyboard.press("Enter");
await page.waitForTimeout(4000);
// Dev verification code (MULTICA_DEV_VERIFICATION_CODE) — OTP boxes consume it
// one character at a time and auto-submit.
await page.locator("input").first().click();
await page.keyboard.type("888888", { delay: 80 });
await page.waitForTimeout(6000);

await page.goto(`${base}/lrm1145/channels/${ctx.chid}`, { waitUntil: "domcontentloaded" });
// A first-login questionnaire sheet can cover the message list — skip it.
for (const name of [/^Skip$/i, /^跳过$/]) {
  const skip = page.getByRole("button", { name }).first();
  if (await skip.isVisible().catch(() => false)) {
    await skip.click().catch(() => {});
    await page.waitForTimeout(1500);
  }
}
try {
  await page.waitForSelector('[data-testid="thread-reply-preview"]', { timeout: 60000 });
} catch (err) {
  await page.screenshot({ path: `${outDir}/${label}-DEBUG.png` });
  console.error(`[${label}] preview never appeared at ${page.url()}`);
  console.error((await page.locator("body").innerText().catch(() => "")).slice(0, 800));
  throw err;
}
await page.waitForTimeout(1500);

const countText = await page
  .getByTestId("thread-reply-preview-count")
  .first()
  .innerText()
  .catch(() => "(missing)");
console.log(`[${label}] preview count label: ${countText.replace(/\n/g, " ")}`);

await page.screenshot({ path: `${outDir}/${label}-full.png`, fullPage: false });
const bubble = page.locator('[data-testid="thread-reply-preview"]').first();
const box = await bubble.boundingBox();
if (box) {
  await page.screenshot({
    path: `${outDir}/${label}-preview.png`,
    clip: {
      x: Math.max(0, box.x - 56),
      y: Math.max(0, box.y - 150),
      width: Math.min(1440 - Math.max(0, box.x - 56), box.width + 112),
      height: box.height + 170,
    },
  });
}
await browser.close();
console.log(`[${label}] shots written to ${outDir}`);
