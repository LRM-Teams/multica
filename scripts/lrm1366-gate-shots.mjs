/**
 * LRM-1366 gate shots — the real DM sidebar region in real Chromium.
 *
 * What only a browser can show: the reported "blank DM region" is a computed
 * colour identity, not a missing element. In the light theme the shipped
 * `Skeleton` fill `bg-muted` resolves to `--page-bg` `#f6f6f4`, which is also
 * `--sidebar`, so every placeholder row paints at 1.00:1 against its own
 * backdrop. jsdom resolves no custom properties and paints nothing, so the unit
 * spec can only guard class names and token text.
 *
 * Usage (harness on :5366 — `npx vite --config scripts/lrm1366-harness/vite.config.mts`):
 *   node scripts/lrm1366-gate-shots.mjs before   # tokens.css + skeleton.tsx + dm-list.tsx reverted to dev
 *   node scripts/lrm1366-gate-shots.mjs after    # fix applied
 *
 * `before` asserts the defect reproduces: at least one light-theme placeholder
 * sits below the 1.25:1 perceivable-placeholder floor against its backdrop, and
 * the all-pinned DM region renders no body at all.
 * `after` asserts, for every theme × viewport: every placeholder clears 1.25:1,
 * the pending region still shows the skeleton (not an empty CTA), and the
 * all-pinned region carries a visible pointer to the PINNED section.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1366.
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const label = process.argv[2];
if (label !== "before" && label !== "after") {
  throw new Error("usage: node scripts/lrm1366-gate-shots.mjs <before|after>");
}

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1366");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5366/";
const MIN_PLACEHOLDER = 1.25;

const VIEWPORTS = [
  { width: 1440, height: 900, kind: "desktop" },
  { width: 360, height: 780, kind: "narrow" },
];
const THEMES = ["light", "dark"];

/** Computed-pixel contrast of every skeleton bar against its real backdrop. */
const PROBE = `(() => {
  const canvas = document.createElement("canvas");
  canvas.width = 1; canvas.height = 1;
  const ctx = canvas.getContext("2d", { willReadFrequently: true });
  const parse = (value) => {
    if (!value || value === "transparent") return null;
    ctx.clearRect(0, 0, 1, 1);
    ctx.fillStyle = "#000";
    ctx.fillStyle = value;
    ctx.globalCompositeOperation = "copy";
    ctx.fillRect(0, 0, 1, 1);
    ctx.globalCompositeOperation = "source-over";
    const [r, g, b, a] = ctx.getImageData(0, 0, 1, 1).data;
    return { r, g, b, a: a / 255 };
  };
  const over = (fg, bg) => ({
    r: fg.r * fg.a + bg.r * (1 - fg.a),
    g: fg.g * fg.a + bg.g * (1 - fg.a),
    b: fg.b * fg.a + bg.b * (1 - fg.a),
    a: 1,
  });
  const lin = (c) => {
    const v = c / 255;
    return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  };
  const lum = (c) => 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
  const ratio = (a, b) => {
    const la = lum(a); const lb = lum(b);
    return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
  };
  const rgbStr = (c) =>
    "rgb(" + Math.round(c.r) + ", " + Math.round(c.g) + ", " + Math.round(c.b) + ")";
  const backdropOf = (el) => {
    const stack = [];
    let n = el.parentElement;
    while (n) {
      const p = parse(getComputedStyle(n).backgroundColor);
      if (p && p.a > 0) stack.push(p);
      n = n.parentElement;
    }
    let bg = { r: 255, g: 255, b: 255, a: 1 };
    for (let i = stack.length - 1; i >= 0; i -= 1) {
      bg = stack[i].a >= 1 ? stack[i] : over(stack[i], bg);
    }
    return bg;
  };
  const bars = [...document.querySelectorAll("[data-slot=skeleton]")];
  const measured = bars.map((el) => {
    const fill = parse(getComputedStyle(el).backgroundColor);
    const backdrop = backdropOf(el);
    const box = el.getBoundingClientRect();
    return {
      fill: fill ? rgbStr(fill) : null,
      backdrop: rgbStr(backdrop),
      ratio: fill ? Number(ratio(fill, backdrop).toFixed(3)) : null,
      visible: box.width > 0 && box.height > 0,
    };
  });
  const region = document.querySelector("[data-testid=dm-list-skeleton]")
    ? "skeleton"
    : document.querySelector("[data-testid=dm-list-all-pinned]")
      ? "all-pinned-hint"
      : null;
  const dmSection = document.querySelector("[aria-expanded]")?.closest("div.pb-1");
  return {
    bars: measured,
    barCount: measured.length,
    worstRatio: measured.length
      ? Math.min(...measured.map((b) => b.ratio ?? 99))
      : null,
    region,
    // Rendered text of the DM region, so "nothing but heading + `+`" is a fact,
    // not an impression.
    regionText: (dmSection?.textContent ?? "").replace(/\\s+/g, " ").trim(),
    skeletonToken: getComputedStyle(document.documentElement)
      .getPropertyValue("--skeleton")
      .trim() || null,
  };
})()`;

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});

const results = [];
const failures = [];

for (const viewport of VIEWPORTS) {
  for (const theme of THEMES) {
    for (const state of ["pending", "all-pinned"]) {
      const context = await browser.newContext({
        viewport: { width: viewport.width, height: viewport.height },
        colorScheme: theme,
        deviceScaleFactor: 2,
      });
      const page = await context.newPage();
      await page.goto(`${base}?theme=${theme}&state=${state}`, {
        // `state=pending` holds GET /api/dm open on purpose — networkidle never
        // fires, which is exactly the window under test.
        waitUntil: "domcontentloaded",
      });
      await page.waitForSelector("aside", { timeout: 15000 });
      // Freeze the pulse so the shot is reproducible, then let layout settle.
      await page.addStyleTag({
        content: "*,*::before,*::after{animation:none!important;transition:none!important}",
      });
      await page.waitForTimeout(600);

      const probe = await page.evaluate(PROBE);
      const name = `${label}-${state}-${viewport.kind}-${viewport.width}-${theme}`;
      await page.locator("aside").screenshot({ path: resolve(outDir, `${name}.png`) });
      results.push({ name, state, theme, viewport: viewport.kind, ...probe });

      if (state === "pending") {
        if (probe.region !== "skeleton") {
          failures.push(`${name}: pending region is ${probe.region}, expected skeleton`);
        }
        if (label === "after" && (probe.worstRatio ?? 0) < MIN_PLACEHOLDER) {
          failures.push(
            `${name}: placeholder contrast ${probe.worstRatio} < ${MIN_PLACEHOLDER}`,
          );
        }
      }
      if (state === "all-pinned" && label === "after") {
        if (probe.region !== "all-pinned-hint") {
          failures.push(`${name}: all-pinned region is ${probe.region}, expected the hint`);
        }
      }
      await context.close();
    }
  }
}

await browser.close();

const lightPending = results.filter((r) => r.theme === "light" && r.state === "pending");
const allPinned = results.filter((r) => r.state === "all-pinned");

if (label === "before") {
  const invisible = lightPending.filter((r) => (r.worstRatio ?? 99) < MIN_PLACEHOLDER);
  if (invisible.length === 0) {
    failures.push("before: light placeholders were already perceivable — defect not reproduced");
  }
  const silent = allPinned.filter((r) => r.region === null);
  if (silent.length === 0) {
    failures.push("before: all-pinned region already rendered a body — hole not reproduced");
  }
}

writeFileSync(
  resolve(outDir, `${label}-probe.json`),
  `${JSON.stringify({ label, minPlaceholder: MIN_PLACEHOLDER, results, failures }, null, 2)}\n`,
);

for (const row of results) {
  console.log(
    `${row.name}: bars=${row.barCount} worstRatio=${row.worstRatio} region=${row.region} ` +
      `token=${row.skeletonToken} text="${row.regionText.slice(0, 60)}"`,
  );
}

if (failures.length) {
  console.error(`\n${label} FAILED:\n- ${failures.join("\n- ")}`);
  process.exit(1);
}
console.log(`\n${label} OK — shots + probe in ${outDir}`);
