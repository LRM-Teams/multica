/**
 * LRM-1244 gate shots — completion-card initial focus must not park on an
 * invisible full-viewport dismiss scrim (same root cause as LRM-1234/1243).
 *
 * Run: pnpm vite --config scripts/lrm1164-harness/vite.config.mts   (port 5201)
 *      MODE=after node scripts/lrm1244-gate-shots.mjs
 *      # for BEFORE (origin/dev with scrim): MODE=before …
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const mode = process.env.MODE === "before" ? "before" : "after";
const outDir = resolve(root, `e2e/artifacts/lrm1244/${mode}`);
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5201/";
const tiers = [
  { width: 375, label: "narrow" },
  { width: 767, label: "deadzone-edge" },
  { width: 1280, label: "desktop" },
];

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});
const page = await browser.newPage();
const errors = [];
const ignorable = (t) => t.includes("favicon.ico") || t.includes("/api/");
page.on("pageerror", (e) => errors.push(String(e)));
page.on("console", (m) => {
  if (m.type() === "error" && !ignorable(m.location().url + m.text())) {
    errors.push(m.text());
  }
});

const fail = (msg) => {
  throw new Error(msg);
};

const describeActive = () =>
  page.evaluate(() => {
    const el = document.activeElement;
    if (!el || el === document.body) {
      return {
        tag: "BODY",
        testid: null,
        name: null,
        rect: { w: 0, h: 0 },
        fullBleed: false,
      };
    }
    const r = el.getBoundingClientRect();
    return {
      tag: el.tagName,
      testid: el.getAttribute("data-testid"),
      name: (el.getAttribute("aria-label") || el.textContent || "")
        .trim()
        .slice(0, 32),
      rect: { w: Math.round(r.width), h: Math.round(r.height) },
      fullBleed:
        /absolute/.test(el.className || "") && /inset-0/.test(el.className || ""),
    };
  });

const readA11y = () =>
  page.evaluate(() => ({
    fullBleedButtons: document.querySelectorAll(
      "dialog button.absolute.inset-0",
    ).length,
    exposedDismiss: Array.from(document.querySelectorAll("button")).filter(
      (b) =>
        (b.getAttribute("aria-label") || "").trim() === "关闭" &&
        b.getAttribute("aria-hidden") !== "true" &&
        !b.closest('[aria-hidden="true"]'),
    ).length,
    cardPresent: Boolean(
      document.querySelector('[data-testid="research-completion-card"]'),
    ),
  }));

const openFresh = async () => {
  await page.goto(`${base}?case=completion`, { waitUntil: "networkidle" });
  await page.waitForSelector('[data-testid="research-completion-card"]');
};

const report = [];

for (const tier of tiers) {
  await page.setViewportSize({ width: tier.width, height: 860 });
  await openFresh();

  const initial = await describeActive();
  const a11y = await readA11y();

  if (!a11y.cardPresent) fail(`@${tier.width}: completion card missing`);

  if (mode === "after") {
    if (a11y.fullBleedButtons !== 0) {
      fail(
        `@${tier.width}: ${a11y.fullBleedButtons} full-bleed dismiss button(s) still present`,
      );
    }
    if (a11y.exposedDismiss !== 1) {
      fail(
        `@${tier.width}: expected exactly 1 exposed 关闭, got ${a11y.exposedDismiss}`,
      );
    }
    if (initial.fullBleed || initial.rect.w >= tier.width) {
      fail(
        `@${tier.width}: focus still on full-viewport target → ${JSON.stringify(initial)}`,
      );
    }
  } else {
    // BEFORE: document the defect (scrim present / focus may be full-bleed).
    if (a11y.fullBleedButtons < 1) {
      fail(`@${tier.width}: BEFORE expected a full-bleed dismiss button`);
    }
  }

  const shotPath = resolve(outDir, `${tier.label}-${tier.width}-focus.png`);
  await page.screenshot({ path: shotPath, fullPage: false });

  report.push({
    tier: tier.width,
    label: tier.label,
    mode,
    initial,
    a11y,
    shot: shotPath,
  });
}

if (errors.length) {
  fail(`page errors: ${errors.join(" | ")}`);
}

writeFileSync(resolve(outDir, "report.json"), JSON.stringify(report, null, 2));
console.log(JSON.stringify({ mode, report }, null, 2));
await browser.close();
