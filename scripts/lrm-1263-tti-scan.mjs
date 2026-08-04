/**
 * LRM-1263 — prod-caliber channel-shell TTI probe (next start / local prod).
 *
 * Ready signal: channel title button aria-label "打开频道详情" / "Open channel details".
 * Reports desktop + narrow first-interactive ms. Does not change product UI.
 *
 * Usage:
 *   BASE_URL=http://127.0.0.1:PORT TOKEN=... SLUG=ws \
 *     node scripts/lrm-1263-tti-scan.mjs
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm-1263");
mkdirSync(outDir, { recursive: true });

const base = (process.env.BASE_URL || "http://127.0.0.1:3000").replace(/\/$/, "");
const token = process.env.TOKEN || process.env.MULTICA_TOKEN || "";
const slug = process.env.SLUG || process.env.WORKSPACE_SLUG || "";
if (!token) throw new Error("TOKEN / MULTICA_TOKEN required");
if (!slug) throw new Error("SLUG / WORKSPACE_SLUG required");

const READY = /打开频道详情|Open channel details/;

async function measure(viewport, label) {
  const browser = await chromium.launch({
    executablePath: process.env.CHROME_BIN || undefined,
  });
  const page = await browser.newPage({ viewport });
  await page.goto(`${base}/login`, { waitUntil: "domcontentloaded", timeout: 120_000 });
  await page.evaluate((t) => {
    localStorage.setItem("multica_token", t);
  }, token);
  const t0 = Date.now();
  await page.goto(`${base}/${slug}/channels`, {
    waitUntil: "domcontentloaded",
    timeout: 120_000,
  });
  // Skip onboarding follow-up if present
  const skip = page.getByRole("button", { name: /跳过|Skip/i });
  if (await skip.isVisible({ timeout: 2_000 }).catch(() => false)) {
    await skip.click();
  }
  await page.getByRole("button", { name: READY }).first().waitFor({
    state: "visible",
    timeout: 120_000,
  });
  const ms = Date.now() - t0;
  const shot = resolve(outDir, `${label}.png`);
  await page.screenshot({ path: shot, fullPage: false });
  await browser.close();
  return { label, ms, shot, budgetMs: 2000, pass: ms < 2000 };
}

const rows = [];
rows.push(await measure({ width: 1440, height: 900 }, "desktop"));
rows.push(await measure({ width: 390, height: 844 }, "narrow"));

const report = {
  base,
  slug,
  mode: "prod-caliber (caller must serve next start)",
  rows,
  allPass: rows.every((r) => r.pass),
  at: new Date().toISOString(),
};
writeFileSync(resolve(outDir, "report.json"), JSON.stringify(report, null, 2));
console.log(JSON.stringify(report, null, 2));
process.exit(report.allPass ? 0 : 2);
