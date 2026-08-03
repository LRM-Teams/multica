/**
 * LRM-1234 gate shots — where does a real browser park focus when the report
 * modal opens, and can focus ever land on an invisible full-viewport control?
 *
 * jsdom cannot answer this: it does not implement the native `<dialog>`
 * focusing steps, so only a real Chromium run proves the defect and the fix.
 * Baseline on dev: `showModal()` focused the full-screen invisible
 * `<button aria-label="关闭" class="absolute inset-0">` — 375×860 focus target,
 * no visible ring, and the first Enter/Space closed the whole report.
 *
 * Asserted on the live DOM before every PNG:
 *   - initial `document.activeElement` is a REAL control (box < viewport)
 *   - no focusable full-bleed layer exists (`button.absolute.inset-0`)
 *   - Shift+Tab out of the first stop cannot reach such a layer
 *   - exactly ONE exposed "关闭" control (the header X)
 *   - LRM-1212 contract still holds: Enter opens the outline drawer, focus
 *     moves inside, Escape closes the drawer only and returns focus
 *
 * Temporary tooling: delete after the shots are attached to LRM-1234.
 *
 * Run: pnpm vite --config scripts/lrm1164-harness/vite.config.mts   (port 5201)
 *      node scripts/lrm1234-gate-shots.mjs
 */
import { mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1234");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5201/";
const tiers = [
  { width: 375, label: "narrow", firstStop: "research-report-outline-toggle" },
  { width: 767, label: "deadzone-edge", firstStop: "research-report-outline-toggle" },
  // >= 768 the outline toggle is `md:hidden`, so copy-markdown is first.
  { width: 1280, label: "desktop", firstStop: null },
];

const browser = await chromium.launch({ executablePath: process.env.CHROME_BIN || undefined });
const page = await browser.newPage();
const errors = [];
const ignorable = (t) => t.includes("favicon.ico") || t.includes("/api/");
page.on("pageerror", (e) => errors.push(String(e)));
page.on("console", (m) => {
  if (m.type() === "error" && !ignorable(m.location().url + m.text())) errors.push(m.text());
});

const fail = (msg) => {
  throw new Error(msg);
};

const describeActive = () =>
  page.evaluate(() => {
    const el = document.activeElement;
    if (!el || el === document.body) {
      return { tag: "BODY", testid: null, name: null, rect: { w: 0, h: 0 }, fullBleed: false };
    }
    const r = el.getBoundingClientRect();
    return {
      tag: el.tagName,
      testid: el.getAttribute("data-testid"),
      name: (el.getAttribute("aria-label") || el.textContent || "").trim().slice(0, 24),
      rect: { w: Math.round(r.width), h: Math.round(r.height) },
      fullBleed:
        /absolute/.test(el.className || "") && /inset-0/.test(el.className || ""),
    };
  });

const readA11y = () =>
  page.evaluate(() => ({
    fullBleedFocusables: document.querySelectorAll(
      'dialog button.absolute.inset-0, dialog [tabindex]:not([tabindex="-1"]).absolute.inset-0',
    ).length,
    exposedClose: Array.from(document.querySelectorAll("button")).filter(
      (b) =>
        (b.getAttribute("aria-label") || "").trim() === "关闭" &&
        b.getAttribute("aria-hidden") !== "true" &&
        !b.closest('[aria-hidden="true"]'),
    ).length,
    cardPresent: Boolean(document.querySelector('[data-testid="research-delivery-modal-card"]')),
  }));

const openFresh = async () => {
  await page.goto(`${base}?case=report`, { waitUntil: "networkidle" });
  await page.waitForSelector('[data-testid="research-delivery-modal-card"]');
};

const report = [];

for (const tier of tiers) {
  await page.setViewportSize({ width: tier.width, height: 860 });
  // Reload per tier: Chrome keeps a sequential focus navigation starting point
  // that `blur()` does not reset, so probes must always start fresh.
  await openFresh();

  const initial = await describeActive();
  const a11y = await readA11y();

  if (!a11y.cardPresent) fail(`@${tier.width}: report card missing`);
  if (a11y.fullBleedFocusables !== 0) {
    fail(`@${tier.width}: ${a11y.fullBleedFocusables} focusable full-bleed layer(s) still inside the dialog`);
  }
  if (a11y.exposedClose !== 1) {
    fail(`@${tier.width}: expected exactly 1 exposed 关闭 control, got ${a11y.exposedClose}`);
  }
  if (initial.fullBleed || initial.rect.w >= tier.width) {
    fail(`@${tier.width}: modal opened with focus on an invisible full-viewport target → ${JSON.stringify(initial)}`);
  }
  if (tier.firstStop) {
    if (initial.testid !== tier.firstStop) {
      fail(`@${tier.width}: initial focus = ${JSON.stringify(initial)}, expected ${tier.firstStop}`);
    }
  } else if (!/复制/.test(initial.name ?? "")) {
    fail(`@${tier.width}: initial focus = ${JSON.stringify(initial)}, expected the copy-markdown button`);
  }

  // Shift+Tab must not fall back onto a full-bleed layer either.
  await page.keyboard.press("Shift+Tab");
  const back = await describeActive();
  if (back.fullBleed || (back.tag !== "BODY" && back.rect.w >= tier.width)) {
    fail(`@${tier.width}: Shift+Tab reached an invisible full-viewport target → ${JSON.stringify(back)}`);
  }

  // Pointer close contract: the topmost element in the gutter must be the
  // dialog itself (that is what the new onClick compares against), and the
  // card must NOT report the dialog as the hit target.
  const hit = await page.evaluate(() => {
    const top = document.elementFromPoint(6, 6);
    const card = document.querySelector('[data-testid="research-delivery-modal-card"]');
    const r = card.getBoundingClientRect();
    const inCard = document.elementFromPoint(
      Math.round(r.left + r.width / 2),
      Math.round(r.top + 4),
    );
    return {
      gutter: top?.tagName ?? null,
      gutterIsDialog: top?.tagName === "DIALOG",
      insideCardIsDialog: inCard?.tagName === "DIALOG",
    };
  });
  if (tier.width >= 768 && !hit.gutterIsDialog) {
    fail(`@${tier.width}: gutter hit target is ${hit.gutter}, expected DIALOG (empty-space close would break)`);
  }
  if (hit.insideCardIsDialog) {
    fail(`@${tier.width}: a point inside the card resolves to the dialog → clicking content would close`);
  }

  // Shot 1: focus ring parked on the initial control.
  await openFresh();
  await page.screenshot({
    path: resolve(outDir, `after-${tier.width}-${tier.label}-initial-focus.png`),
  });

  const tabSequence = [];
  for (let i = 0; i < 5; i += 1) {
    await page.keyboard.press("Tab");
    tabSequence.push((await describeActive()).testid ?? (await describeActive()).name);
  }

  // LRM-1212 regression: keyboard drawer open/close + focus return (narrow only).
  let escape = null;
  if (tier.firstStop) {
    await openFresh();
    await page.keyboard.press("Enter");
    await page.waitForSelector('[data-testid="research-report-outline-drawer"]');
    const inDrawer = await page.evaluate(() =>
      Boolean(
        document
          .querySelector('[data-testid="research-report-outline-drawer"]')
          ?.contains(document.activeElement),
      ),
    );
    if (!inDrawer) fail(`@${tier.width}: drawer opened but focus stayed outside it`);
    await page.screenshot({
      path: resolve(outDir, `after-${tier.width}-${tier.label}-drawer-open.png`),
    });

    await page.keyboard.press("Escape");
    await page.waitForTimeout(120);
    escape = await page.evaluate(() => ({
      drawer: Boolean(document.querySelector('[data-testid="research-report-outline-drawer"]')),
      modal: Boolean(document.querySelector('[data-testid="research-delivery-modal-card"]')),
      focus: document.activeElement?.getAttribute("data-testid") ?? null,
    }));
    if (escape.drawer) fail(`@${tier.width}: Escape did not close the drawer`);
    if (!escape.modal) fail(`@${tier.width}: Escape closed the modal instead of the drawer`);
    if (escape.focus !== "research-report-outline-toggle") {
      fail(`@${tier.width}: focus not returned to the toggle → ${JSON.stringify(escape)}`);
    }
  }

  report.push({ tier: tier.width, initialFocus: initial, shiftTabBack: back, hitTest: hit, tabSequence, a11y, escape });
}

if (errors.length) fail(`console/page errors: ${errors.join(" | ")}`);

console.log(JSON.stringify(report, null, 2));
console.log(`\nPASS — shots in ${outDir}`);
await browser.close();
