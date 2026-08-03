/**
 * LRM-1170 gate shots — real-component render, desktop (1280) + narrow (375).
 * Asserts the AC invariants on the live DOM, then writes one PNG per state.
 * Temporary tooling: delete after the shots are attached to the issue.
 */
import { mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1170");
mkdirSync(outDir, { recursive: true });

const url = process.env.HARNESS_URL ?? "http://localhost:5199/";
const cases = [
  "options-pending",
  "options-inflight",
  "options-resolved-answered",
  "options-resolved-skipped",
  "form-pending",
  "form-resolved-answered",
];
const viewports = [
  { name: "desktop-1280", width: 1280, height: 900 },
  { name: "narrow-375", width: 375, height: 900 },
];

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});
const page = await browser.newPage();
const errors = [];
const ignorable = (text) => text.includes("favicon.ico");
page.on("pageerror", (e) => errors.push(String(e)));
page.on("console", (m) => {
  if (m.type() === "error" && !ignorable(m.location().url + m.text())) errors.push(m.text());
});
page.on("requestfailed", (r) => {
  if (!ignorable(r.url())) errors.push(`requestfailed ${r.url()}`);
});

await page.goto(url, { waitUntil: "networkidle" });
await page.waitForSelector('[data-case="options-pending"] [data-testid="research-clarification-card"]');

const report = [];
for (const vp of viewports) {
  await page.setViewportSize({ width: vp.width, height: vp.height });
  await page.waitForTimeout(150);
  for (const id of cases) {
    const scope = page.locator(`[data-case="${id}"]`);
    const card = scope.locator('[data-testid="research-clarification-card"]');

    // AC1 — at most one opacity utility per option, and never on the picked one.
    const optionClasses = await scope
      .locator('[data-testid="research-clarification-option"]')
      .evaluateAll((els) =>
        els.map((el) => ({
          id: el.getAttribute("data-option-id"),
          pressed: el.getAttribute("aria-pressed"),
          opacity: el.className.split(/\s+/).filter((c) => c.startsWith("opacity-")),
          computed: getComputedStyle(el).opacity,
        })),
      );
    for (const o of optionClasses) {
      if (o.opacity.length > 1) throw new Error(`${id}/${vp.name}: option ${o.id} has ${o.opacity.join("+")}`);
      if (o.pressed === "true" && o.opacity.length > 0)
        throw new Error(`${id}/${vp.name}: selected option ${o.id} dimmed by ${o.opacity.join("+")}`);
    }

    // AC2 — skip is gone once settled, in both layouts.
    const skipCount = await scope.locator('[data-testid="research-clarification-skip"]').count();
    const settled = id.includes("resolved");
    if (settled && skipCount !== 0) throw new Error(`${id}/${vp.name}: skip still rendered`);
    if (!settled && skipCount !== 1) throw new Error(`${id}/${vp.name}: skip missing while pending`);

    const box = await card.boundingBox();
    const optionHeights = await scope
      .locator('[data-testid="research-clarification-option"]')
      .evaluateAll((els) => els.map((el) => Math.round(el.getBoundingClientRect().height)));
    const caption = scope.locator(
      '[data-testid="research-clarification-answered"], [data-testid="research-clarification-skipped"]',
    );
    const captionBlock = (await caption.count())
      ? await caption.evaluate((el) => {
          const mt = Number.parseFloat(getComputedStyle(el).marginTop) || 0;
          return Math.round(el.getBoundingClientRect().height + mt);
        })
      : 0;
    report.push({
      case: id,
      viewport: vp.name,
      cardHeight: box ? Math.round(box.height) : null,
      optionHeights,
      captionBlock,
      optionOpacities: optionClasses.map((o) => `${o.id}:${o.computed}`).join(" "),
      skip: skipCount,
    });

    const path = resolve(outDir, `${id}--${vp.name}.png`);
    await card.screenshot({ path });
  }
}

// AC4 — the dim change must not resize anything: option rows keep their exact
// height from pending to settled, and every card-height delta is attributable
// to the two intentional swaps (skip button out, answered/skipped caption in).
for (const vp of viewports) {
  const row = (c) => report.find((r) => r.case === c && r.viewport === vp.name);
  for (const settledCase of ["options-resolved-answered", "options-resolved-skipped"]) {
    const before = row("options-pending");
    const after = row(settledCase);
    if (JSON.stringify(before.optionHeights) !== JSON.stringify(after.optionHeights))
      throw new Error(
        `${vp.name}: option heights moved ${before.optionHeights} -> ${after.optionHeights} (${settledCase})`,
      );
  }
  for (const [pendingCase, settledCase] of [
    ["options-pending", "options-resolved-answered"],
    ["options-pending", "options-resolved-skipped"],
    ["form-pending", "form-resolved-answered"],
  ]) {
    const before = row(pendingCase);
    const after = row(settledCase);
    const delta = after.cardHeight - before.cardHeight;
    // Skip teardown can only remove height; the caption can only add its own
    // block. So the delta must sit inside [-(skip block), +caption block].
    if (delta > after.captionBlock)
      throw new Error(
        `${vp.name}: ${settledCase} grew ${delta}px, more than the caption block (${after.captionBlock}px)`,
      );
  }
}

if (errors.length) throw new Error(`console/page errors: ${errors.join(" | ")}`);

console.log(JSON.stringify(report, null, 2));
console.log("shots ->", outDir);
await browser.close();
