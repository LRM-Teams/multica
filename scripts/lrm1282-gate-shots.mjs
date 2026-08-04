/**
 * LRM-1282 gate shots — real SourceStrategyStrip + HumanBoundaryCard.
 *
 * 12 frames: 1440 (drawer 360) + 390 (full sheet width) × light/dark ×
 * loading / empty / ready. Loading frames must show expected-outcome copy;
 * ready frames must show real seed facts (not placeholders).
 *
 * Temporary tooling: delete after the shots are attached to LRM-1282.
 */
import { mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1282");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5282/";
const themes = ["light", "dark"];
const modes = ["loading", "empty", "ready"];
const viewports = [
  { name: "1440", width: 1440, height: 900 },
  { name: "390", width: 390, height: 844 },
];

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});

const errors = [];
const report = [];

for (const vp of viewports) {
  const page = await browser.newPage({
    viewport: { width: vp.width, height: vp.height },
  });
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("console", (m) => {
    if (
      m.type() === "error" &&
      !m.text().includes("favicon") &&
      !m.text().includes("404 (Not Found)")
    ) {
      errors.push(m.text());
    }
  });

  for (const theme of themes) {
    for (const mode of modes) {
      const url = `${base}?theme=${theme}&mode=${mode}`;
      await page.goto(url, { waitUntil: "networkidle" });
      await page.waitForSelector('[data-testid="source-strategy-strip"]');
      await page.waitForSelector('[data-testid="human-boundary-card"]');
      await page.waitForTimeout(120);

      const probe = await page.evaluate(() => {
        const strategy = document.querySelector(
          '[data-testid="source-strategy-strip"]',
        );
        const boundary = document.querySelector(
          '[data-testid="human-boundary-card"]',
        );
        const expected =
          document.querySelector('[data-testid="source-strategy-expected"]') ||
          document.querySelector('[data-testid="human-boundary-expected"]');
        const cards = document.querySelector(
          '[data-testid="source-strategy-cards"]',
        );
        const drawer = document.querySelector(
          '[data-testid="research-aux-drawer-chrome"]',
        );
        const overflowX = document.documentElement.scrollWidth >
          document.documentElement.clientWidth + 1;
        return {
          strategyTitle: strategy?.querySelector("h3")?.textContent ?? "",
          boundaryTitle: boundary?.querySelector("h3")?.textContent ?? "",
          hasExpected: !!expected,
          hasCards: !!cards,
          drawerWidth: drawer
            ? Math.round(drawer.getBoundingClientRect().width)
            : 0,
          overflowX,
          ariaBusy: strategy?.getAttribute("aria-busy"),
        };
      });

      if (!probe.strategyTitle.includes("信源") && !probe.strategyTitle.includes("哪些")) {
        throw new Error(`missing purpose title: ${JSON.stringify(probe)}`);
      }
      if (mode === "loading" && !probe.hasExpected) {
        throw new Error(`loading frame missing expected outcomes @ ${vp.name}/${theme}`);
      }
      if (mode === "ready" && !probe.hasCards) {
        throw new Error(`ready frame missing real cards @ ${vp.name}/${theme}`);
      }
      if (mode === "loading" && probe.ariaBusy !== "true") {
        throw new Error(`loading frame aria-busy!=true @ ${vp.name}/${theme}`);
      }
      if (vp.name === "1440" && probe.drawerWidth > 380) {
        throw new Error(`desktop drawer wider than 360: ${probe.drawerWidth}`);
      }
      if (probe.overflowX) {
        throw new Error(`horizontal overflow @ ${vp.name}/${theme}/${mode}`);
      }

      const file = `${vp.name}--${theme}--${mode}.png`;
      await page.screenshot({ path: resolve(outDir, file), fullPage: false });
      report.push({ file, ...probe });
    }
  }

  await page.close();
}

await browser.close();

if (errors.length) {
  console.error(errors);
  throw new Error(`page errors during gate shots: ${errors.join(" | ")}`);
}

console.log(JSON.stringify({ ok: true, count: report.length, report }, null, 2));
