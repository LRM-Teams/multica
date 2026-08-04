/**
 * LRM-1227 BEFORE probe — origin/dev has no shell node, so it measures what
 * origin/dev actually paints: the row hover wash, the 184px gutter, and the
 * 767px fine-pointer cell where the gutter's extra `md:` gate drops out and the
 * action bar lands on the body text.
 */
import { mkdirSync, writeFileSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1227");
mkdirSync(outDir, { recursive: true });
const base = process.env.SHOT_BASE_URL ?? "http://localhost:13760";
const ctx = JSON.parse(readFileSync("/tmp/lrm1227-ctx.json", "utf8"));
const url = `${base}/${ctx.workspaceSlug}/channels/${ctx.chid}`;

const browser = await chromium.launch();
const out = { label: "before", url, cells: {} };

async function cell(width, height, name, coarse = false) {
  const page = await browser.newPage({
    viewport: { width, height },
    locale: "zh-CN",
    storageState: "/tmp/lrm1227-state.json",
    ...(coarse ? { hasTouch: true, isMobile: true, deviceScaleFactor: 2 } : {}),
  });
  page.setDefaultTimeout(120_000);
  page.setDefaultNavigationTimeout(180_000);
  await page.goto(url, { waitUntil: "domcontentloaded" });
  await page.locator('[data-testid="message-bubble"]').first().waitFor({ timeout: 120_000 });
  await page.waitForTimeout(2500);

  const lead = page.locator('[data-testid="message-row"][data-message-group="lead"]').first();
  if (!coarse) {
    await lead.hover();
    await page.waitForTimeout(600);
  }
  out.cells[name] = await page.evaluate(() => {
    const row = document.querySelector('[data-testid="message-row"][data-message-group="lead"]');
    const bubble = row?.querySelector('[data-testid="message-bubble"]');
    const body = row?.querySelector('[data-testid="message-body"]');
    const col = body?.parentElement ?? null;
    const bar = row?.querySelector('[data-testid="message-action-bar"]');
    const rows = Array.from(document.querySelectorAll('[data-testid="message-row"]'));
    const leadTops = rows
      .filter((r) => r.dataset.messageGroup === "lead")
      .map((r) => r.getBoundingClientRect().top);
    const pitches = [];
    for (let i = 1; i < leadTops.length; i += 1) pitches.push(Math.round(leadTops[i] - leadTops[i - 1]));
    const res = {
      media: {
        pointerFine: matchMedia("(pointer: fine)").matches,
        pointerCoarse: matchMedia("(pointer: coarse)").matches,
        innerWidth,
      },
      rowCount: rows.length,
      pitches,
      hasShellNode: !!document.querySelector('[data-testid="message-shell"]'),
      bubbleBackground: bubble ? getComputedStyle(bubble).backgroundColor : null,
      gutterRight: col ? getComputedStyle(col).paddingRight : null,
      contentBorder: col ? getComputedStyle(col).borderTopWidth : null,
      bodyWidth: body ? Math.round(body.getBoundingClientRect().width) : null,
    };
    if (bar && body) {
      const b = bar.getBoundingClientRect();
      const t = body.getBoundingClientRect();
      res.bar = {
        size: `${Math.round(b.width)}x${Math.round(b.height)}`,
        visible: getComputedStyle(bar).display !== "none",
        opacity: getComputedStyle(bar).opacity,
        border: `${getComputedStyle(bar).borderTopWidth} ${getComputedStyle(bar).borderTopColor}`,
        controls: bar.querySelectorAll("button").length,
        left: Math.round(b.left),
        bodyRight: Math.round(t.right),
        overlapsBody:
          b.left < t.right && b.right > t.left && b.bottom > t.top && b.top < t.bottom,
      };
    }
    return res;
  });
  await page.screenshot({ path: resolve(outDir, `lrm1227-before-${name}.png`) });
  await page.close();
}

await cell(1440, 900, "1440-fine-hover");
await cell(767, 900, "767-fine-hover");
await cell(360, 780, "360-coarse", true);

await browser.close();
writeFileSync(resolve(outDir, "lrm1227-before-report.json"), JSON.stringify(out, null, 2));
console.log(JSON.stringify(out.cells, null, 2));
