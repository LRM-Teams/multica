#!/usr/bin/env node
/**
 * LRM-1227 gate shots + geometry probe — real Chromium against the real app,
 * reusing the seeded session from `lrm1227-shell-seed.mjs`.
 *
 * Frames: 1440 fine (non-hover + hover), 360 coarse, 767 fine (the gutter/bar
 * same-gate cell), date-divider adjacency, self-mention row, keyboard
 * focus-within. Plus a measured report, because "the bar rides the shell edge"
 * is a number, not an adjective:
 *   bar.right − shell.right, bar mid-line − shell.top, group pitch, body width,
 *   and the hover edge colour actually painted.
 *
 * Usage: SHOT_LABEL=after node scripts/lrm1227-shell-shots.mjs
 * Temporary tooling: delete once the shots are attached to LRM-1227.
 */
import { mkdirSync, writeFileSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const label = process.env.SHOT_LABEL ?? "after";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1227");
mkdirSync(outDir, { recursive: true });

const base = process.env.SHOT_BASE_URL ?? "http://localhost:13760";
const ctx = JSON.parse(readFileSync("/tmp/lrm1227-ctx.json", "utf8"));
const url = `${base}/${ctx.workspaceSlug}/channels/${ctx.chid}`;

const browser = await chromium.launch();
const report = { label, url, cells: {} };

async function open({ width, height, coarse }) {
  const page = await browser.newPage({
    viewport: { width, height },
    locale: "zh-CN",
    storageState: "/tmp/lrm1227-state.json",
    // `hasTouch` alone does not flip the media query; Playwright derives
    // `pointer: coarse` from `isMobile`/`hasTouch` on Chromium.
    ...(coarse ? { hasTouch: true, isMobile: true, deviceScaleFactor: 2 } : {}),
  });
  page.setDefaultTimeout(120_000);
  page.setDefaultNavigationTimeout(180_000);
  await page.goto(url, { waitUntil: "domcontentloaded" });
  await page.locator('[data-testid="message-bubble"]').first().waitFor({ timeout: 120_000 });
  await page.waitForTimeout(2500);
  return page;
}

const measure = (page) =>
  page.evaluate(() => {
    const rows = Array.from(document.querySelectorAll('[data-testid="message-row"]'));
    const read = (row) => {
      const bubble = row.querySelector('[data-testid="message-bubble"]');
      const shell = row.querySelector('[data-testid="message-shell"]');
      const bar = row.querySelector('[data-testid="message-action-bar"]');
      const body = row.querySelector('[data-testid="message-body"]');
      const rect = (el) => {
        if (!el) return null;
        const r = el.getBoundingClientRect();
        return { top: r.top, right: r.right, left: r.left, bottom: r.bottom, w: r.width, h: r.height };
      };
      const cs = shell ? getComputedStyle(shell) : null;
      return {
        group: row.dataset.messageGroup,
        groupStart: shell?.dataset.groupStart ?? null,
        groupEnd: shell?.dataset.groupEnd ?? null,
        selfMentioned: bubble?.dataset.selfMentioned ?? null,
        row: rect(row),
        shell: rect(shell),
        bar: rect(bar),
        body: rect(body),
        shellStyle: cs && {
          background: cs.backgroundColor,
          borderTop: `${cs.borderTopWidth} ${cs.borderTopColor}`,
          borderBottom: `${cs.borderBottomWidth} ${cs.borderBottomColor}`,
          borderLeft: `${cs.borderLeftWidth} ${cs.borderLeftColor}`,
          radius: `${cs.borderTopLeftRadius}/${cs.borderBottomLeftRadius}`,
          paddingRight: cs.paddingRight,
          paddingLeft: cs.paddingLeft,
        },
      };
    };
    const measured = rows.map(read);
    // Group pitch = distance between consecutive lead rows of the same size.
    const leadTops = measured.filter((m) => m.group === "lead" && m.row).map((m) => m.row.top);
    const pitches = [];
    for (let i = 1; i < leadTops.length; i += 1) pitches.push(Math.round(leadTops[i] - leadTops[i - 1]));
    return {
      media: {
        pointerFine: matchMedia("(pointer: fine)").matches,
        pointerCoarse: matchMedia("(pointer: coarse)").matches,
        narrow: matchMedia("(max-width: 767px)").matches,
        innerWidth: innerWidth,
      },
      rowCount: rows.length,
      pitches,
      rows: measured,
      tombstone: !!document.querySelector('[data-testid="message-tombstone"]'),
      dateDividerCount: Array.from(document.querySelectorAll("div,span"))
        .filter((el) => /^(今天|昨天|Today|Yesterday)$/.test((el.textContent ?? "").trim())).length,
    };
  });

const shot = async (page, cell) => {
  await page.screenshot({ path: resolve(outDir, `lrm1227-${label}-${cell}.png`), fullPage: false });
};

// —— 1440 fine: non-hover, then hover on a group head ——
{
  const page = await open({ width: 1440, height: 900 });
  report.cells["1440-fine"] = await measure(page);
  await shot(page, "1440-fine-idle");

  const head = page
    .locator('[data-testid="message-row"]')
    .filter({ has: page.locator('[data-group-start="true"]') })
    .first();
  await head.hover();
  await page.waitForTimeout(600);
  report.cells["1440-fine-hover"] = await page.evaluate(() => {
    const row = document.querySelector('[data-testid="message-row"]:has([data-group-start="true"])');
    const shell = row?.querySelector('[data-testid="message-shell"]');
    const bar = row?.querySelector('[data-testid="message-action-bar"]');
    if (!shell || !bar) return { error: "no shell/bar" };
    const s = shell.getBoundingClientRect();
    const b = bar.getBoundingClientRect();
    const cs = getComputedStyle(shell);
    const cb = getComputedStyle(bar);
    return {
      barRightMinusShellRight: Math.round((b.right - s.right) * 100) / 100,
      barMidMinusShellTop: Math.round((b.top + b.height / 2 - s.top) * 100) / 100,
      barSize: `${Math.round(b.width)}x${Math.round(b.height)}`,
      barOpacity: cb.opacity,
      barBorder: `${cb.borderTopWidth} ${cb.borderTopColor}`,
      shellHoverBorder: `${cs.borderTopWidth} ${cs.borderTopColor}`,
      shellHoverBackground: cs.backgroundColor,
      gutterRight: cs.paddingRight,
      bodyWidth: Math.round(
        row.querySelector('[data-testid="message-body"]')?.getBoundingClientRect().width ?? 0,
      ),
      controls: bar.querySelectorAll("button").length,
    };
  });
  await shot(page, "1440-fine-hover");

  // Keyboard focus-within: Tab into the bar and confirm the ring + the same
  // strengthened edge the mouse gets.
  report.cells["keyboard-focus-within"] = await page.evaluate(async () => {
    const row = document.querySelector('[data-testid="message-row"]:has([data-group-start="true"])');
    const bar = row?.querySelector('[data-testid="message-action-bar"]');
    const first = bar?.querySelector("button");
    if (!first) return { error: "no bar control" };
    first.focus();
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    const shell = row.querySelector('[data-testid="message-shell"]');
    const cs = getComputedStyle(shell);
    return {
      activeElement: document.activeElement?.getAttribute("aria-label") ?? document.activeElement?.tagName,
      focusWithinBorder: `${cs.borderTopWidth} ${cs.borderTopColor}`,
      barOpacity: getComputedStyle(bar).opacity,
      controlSize: `${Math.round(first.getBoundingClientRect().width)}x${Math.round(first.getBoundingClientRect().height)}`,
    };
  });
  await shot(page, "keyboard-focus-within");

  // Self-mention row + date divider adjacency, scrolled into frame.
  const selfRow = page.locator('[data-self-mentioned="true"]').first();
  if (await selfRow.count()) {
    await selfRow.scrollIntoViewIfNeeded();
    await page.waitForTimeout(500);
    await shot(page, "self-mention-row");
  }
  await page.locator('[data-testid="message-bubble"]').first().scrollIntoViewIfNeeded();
  await page.waitForTimeout(800);
  await shot(page, "date-divider-adjacent");
  await page.close();
}

// —— 767 fine: the cell where the gutter used to disappear ——
{
  const page = await open({ width: 767, height: 900 });
  report.cells["767-fine"] = await measure(page);
  const head = page
    .locator('[data-testid="message-row"]')
    .filter({ has: page.locator('[data-group-start="true"]') })
    .first();
  await head.hover();
  await page.waitForTimeout(600);
  report.cells["767-fine-hover"] = await page.evaluate(() => {
    const row = document.querySelector('[data-testid="message-row"]:has([data-group-start="true"])');
    const shell = row?.querySelector('[data-testid="message-shell"]');
    const bar = row?.querySelector('[data-testid="message-action-bar"]');
    const body = row?.querySelector('[data-testid="message-body"]');
    if (!shell || !bar || !body) return { error: "no shell/bar/body" };
    const b = bar.getBoundingClientRect();
    const t = body.getBoundingClientRect();
    // The real bug shape: is the bar sitting on top of the text box?
    const overlapsBody = b.left < t.right && b.right > t.left && b.bottom > t.top && b.top < t.bottom;
    return {
      gutterRight: getComputedStyle(shell).paddingRight,
      barVisible: getComputedStyle(bar).display !== "none",
      barOpacity: getComputedStyle(bar).opacity,
      barLeft: Math.round(b.left),
      bodyRight: Math.round(t.right),
      overlapsBody,
    };
  });
  await shot(page, "767-fine-hover");
  await page.close();
}

// —— 360 coarse: no bar, no gutter, shell still joined ——
{
  const page = await open({ width: 360, height: 780, coarse: true });
  report.cells["360-coarse"] = await measure(page);
  await shot(page, "360-coarse");
  await page.close();
}

await browser.close();
writeFileSync(resolve(outDir, `lrm1227-${label}-report.json`), JSON.stringify(report, null, 2));
const c = report.cells;
console.log(
  JSON.stringify(
    {
      label,
      media1440: c["1440-fine"]?.media,
      rowCount: c["1440-fine"]?.rowCount,
      pitches: c["1440-fine"]?.pitches,
      hover: c["1440-fine-hover"],
      keyboard: c["keyboard-focus-within"],
      cell767: c["767-fine-hover"],
      coarse: { media: c["360-coarse"]?.media, gutter: c["360-coarse"]?.rows?.[0]?.shellStyle?.paddingRight },
    },
    null,
    2,
  ),
);
