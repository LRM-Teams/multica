/**
 * LRM-1164 AC4 gate shots — real-component render in a real browser, so the
 * Tailwind `md:` (768) companions are actually evaluated (jsdom cannot).
 *
 * Asserts the breakpoint contract on the live DOM first, then writes PNGs:
 *   < 768  → report outline = top drawer, toggle visible, body full width;
 *            list row shows folded narrow meta, no desktop columns
 *   >= 768 → report outline = 220px aside, toggle hidden;
 *            list row shows stage/time/people columns
 *   row and skeleton must flip on the SAME tier (zero column jump on load)
 *
 * Temporary tooling: delete after the shots are attached to LRM-1164.
 */
import { mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1164");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5201/";
// Screenshot tiers required by AC4 (700 / 768) plus the 767 dead-zone edge.
const shotWidths = [700, 767, 768];
// Assert-only tiers from AC1 (360 / 1024 / 1440 are contract, not shot budget).
const assertWidths = [360, 700, 767, 768, 1024, 1440];

const browser = await chromium.launch({ executablePath: process.env.CHROME_BIN || undefined });
const page = await browser.newPage();
const errors = [];
const ignorable = (t) => t.includes("favicon.ico") || t.includes("/api/");
page.on("pageerror", (e) => errors.push(String(e)));
page.on("console", (m) => {
  if (m.type() === "error" && !ignorable(m.location().url + m.text())) errors.push(m.text());
});
page.on("requestfailed", (r) => {
  if (!ignorable(r.url())) errors.push(`requestfailed ${r.url()}`);
});

const report = [];
const fail = (msg) => {
  throw new Error(msg);
};

/* ------------------------------- report reader ------------------------------ */
await page.goto(`${base}?case=report`, { waitUntil: "networkidle" });
await page.waitForSelector('[data-testid="research-delivery-modal-card"]');

for (const width of assertWidths) {
  await page.setViewportSize({ width, height: 900 });
  await page.waitForTimeout(120);
  const desktop = width >= 768;

  const geo = await page.evaluate(() => {
    const isVisible = (el) => {
      if (!el) return false;
      const cs = getComputedStyle(el);
      return (
        cs.display !== "none" &&
        cs.visibility !== "hidden" &&
        el.getBoundingClientRect().width > 0
      );
    };
    const card = document.querySelector('[data-testid="research-delivery-modal-card"]');
    const toggle = document.querySelector('[data-testid="research-report-outline-toggle"]');
    const aside = document.querySelector('[data-testid="research-report-outline-aside"]');
    const scroll = document.querySelector('[data-testid="research-report-scroll"]');
    const drawer = document.querySelector('[data-testid="research-report-outline-drawer"]');
    const w = (el) => (el ? Math.round(el.getBoundingClientRect().width) : 0);
    return {
      cardWidth: w(card),
      toggleVisible: isVisible(toggle),
      asideVisible: isVisible(aside),
      asideWidth: w(aside),
      scrollWidth: w(scroll),
      drawerVisible: isVisible(drawer),
    };
  });

  if (desktop) {
    if (geo.toggleVisible) fail(`report@${width}: outline toggle still visible at >=768`);
    if (!geo.asideVisible || geo.asideWidth !== 220)
      fail(`report@${width}: expected 220px aside, got visible=${geo.asideVisible} w=${geo.asideWidth}`);
    if (geo.scrollWidth > geo.cardWidth - 200)
      fail(`report@${width}: body ${geo.scrollWidth}px did not yield the 220px outline column`);
  } else {
    if (!geo.toggleVisible) fail(`report@${width}: outline toggle hidden below 768 (dead zone)`);
    if (geo.asideVisible || geo.asideWidth !== 0)
      fail(`report@${width}: 220px aside leaked below 768 (w=${geo.asideWidth})`);
    if (geo.scrollWidth < geo.cardWidth - 4)
      fail(`report@${width}: body ${geo.scrollWidth}px is not full card width ${geo.cardWidth}px`);
  }
  report.push({ surface: "report", width, ...geo });

  if (!shotWidths.includes(width)) continue;
  await page.screenshot({ path: resolve(outDir, `report-${width}.png`) });

  if (!desktop) {
    // Narrow: the outline must be reachable as a top drawer.
    await page.click('[data-testid="research-report-outline-toggle"]');
    await page.waitForTimeout(150);
    const open = await page.evaluate(() => {
      const isVisible = (el) => {
        if (!el) return false;
        const cs = getComputedStyle(el);
        return (
          cs.display !== "none" &&
          cs.visibility !== "hidden" &&
          el.getBoundingClientRect().width > 0
        );
      };
      const drawer = document.querySelector('[data-testid="research-report-outline-drawer"]');
      const items = drawer ? drawer.querySelectorAll("[data-outline-id]").length : 0;
      return { drawerVisible: isVisible(drawer), items };
    });
    if (!open.drawerVisible || open.items === 0)
      fail(`report@${width}: outline drawer did not open (visible=${open.drawerVisible} items=${open.items})`);
    await page.screenshot({ path: resolve(outDir, `report-${width}-outline-drawer.png`) });
    report.push({ surface: "report-drawer", width, ...open });
    await page.click('[data-testid="research-report-outline-toggle"]');
    await page.waitForTimeout(120);
  }
}

/* ------------------------------- session list ------------------------------- */
await page.goto(`${base}?case=list`, { waitUntil: "networkidle" });
await page.waitForSelector('[data-testid="research-session-row"]');

for (const width of assertWidths) {
  await page.setViewportSize({ width, height: 640 });
  await page.waitForTimeout(120);
  const desktop = width >= 768;

  const state = await page.evaluate(() => {
    const isVisible = (el) => {
      if (!el) return false;
      const cs = getComputedStyle(el);
      return (
        cs.display !== "none" &&
        cs.visibility !== "hidden" &&
        el.getBoundingClientRect().width > 0
      );
    };
    const row = document.querySelector('[data-testid="research-session-row"]');
    const skeleton = document.querySelector('[data-testid="research-session-row-skeleton"]');
    const q = (scope, sel) => Array.from(scope.querySelectorAll(sel));
    return {
      narrowMeta: q(row, "span.inline-flex").filter((el) => el.className.includes("md:hidden")).map(isVisible),
      desktopCols: q(row, "span.hidden").filter((el) => el.className.includes("md:inline")).map(isVisible),
      people: q(row, "span.hidden").filter((el) => el.className.includes("md:flex")).map(isVisible),
      skeletonCols: q(skeleton, ".hidden").filter((el) => el.className.includes("md:block")).map(isVisible),
      skeletonPeople: q(skeleton, "span.hidden").filter((el) => el.className.includes("md:flex")).map(isVisible),
      rowHeight: Math.round(row.getBoundingClientRect().height),
      skeletonHeight: Math.round(skeleton.getBoundingClientRect().height),
    };
  });

  const all = (arr, want) => arr.length > 0 && arr.every((v) => v === want);
  if (desktop) {
    if (!all(state.desktopCols, true)) fail(`list@${width}: desktop stage/time columns missing at >=768`);
    if (!all(state.people, true)) fail(`list@${width}: people stack missing at >=768`);
    if (!all(state.narrowMeta, false)) fail(`list@${width}: folded narrow meta still shown at >=768`);
    if (!all(state.skeletonCols, true) || !all(state.skeletonPeople, true))
      fail(`list@${width}: skeleton columns do not match the row tier at >=768`);
  } else {
    if (!all(state.narrowMeta, true)) fail(`list@${width}: folded narrow meta missing below 768`);
    if (!all(state.desktopCols, false)) fail(`list@${width}: desktop columns leaked below 768 (dead zone)`);
    if (!all(state.people, false)) fail(`list@${width}: people stack leaked below 768`);
    if (!all(state.skeletonCols, false) || !all(state.skeletonPeople, false))
      fail(`list@${width}: skeleton kept desktop columns while the row folded (load-time column jump)`);
  }
  report.push({ surface: "list", width, ...state });

  if (shotWidths.includes(width)) {
    await page.screenshot({ path: resolve(outDir, `list-${width}.png`), fullPage: true });
  }
}

if (errors.length) {
  console.error("page errors:", errors);
  throw new Error(`harness produced ${errors.length} page error(s)`);
}

console.log(JSON.stringify(report, null, 2));
console.log(`\nLRM-1164 gate shots written to ${outDir}`);
await browser.close();
