/**
 * LRM-1216 gate shots — Agents page DM entry (desktop 1280 + narrow 375).
 * Asserts every row + detail header expose `agent-open-dm-button`, then PNGs.
 * Temporary tooling: delete after shots are attached.
 */
import { mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1216");
mkdirSync(outDir, { recursive: true });

const url = process.env.HARNESS_URL ?? "http://localhost:5216/";
const viewports = [
  { name: "desktop-1280", width: 1280, height: 900 },
  { name: "narrow-375", width: 375, height: 900 },
];

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});
const page = await browser.newPage();
const errors = [];
const ignorable = (t) => t.includes("favicon.ico");
page.on("pageerror", (e) => errors.push(String(e)));
page.on("console", (m) => {
  if (m.type() === "error" && !ignorable(m.location().url + m.text())) {
    errors.push(m.text());
  }
});

await page.goto(url, { waitUntil: "networkidle" });
await page.waitForSelector('[data-testid="lrm1216-agents-surface"]');

for (const vp of viewports) {
  await page.setViewportSize({ width: vp.width, height: vp.height });
  await page.waitForTimeout(150);

  const buttons = await page.locator('[data-testid="agent-open-dm-button"]').count();
  // 3 rows (icon) + 1 labeled detail header
  if (buttons < 4) {
    throw new Error(`${vp.name}: expected ≥4 DM entries, got ${buttons}`);
  }

  const first = page.locator('[data-testid="agent-open-dm-button"]').first();
  const box = await first.boundingBox();
  if (!box || box.width < 24 || box.height < 24) {
    throw new Error(`${vp.name}: DM entry too small (${JSON.stringify(box)})`);
  }

  await page.screenshot({
    path: resolve(outDir, `agents-dm-entry-${vp.name}.png`),
    fullPage: true,
  });
  console.log(`wrote agents-dm-entry-${vp.name}.png (dm buttons=${buttons})`);
}

if (errors.length) {
  console.warn("console/page errors (non-fatal for shot):", errors.slice(0, 5));
}

await browser.close();
console.log("LRM-1216 gate shots OK →", outDir);
