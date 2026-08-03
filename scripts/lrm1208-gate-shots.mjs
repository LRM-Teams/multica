/**
 * LRM-1208 gate shots — real `ResearchGitList` in a real browser.
 *
 * jsdom returns the literal string `var(--research-lane-3)` for an SVG
 * `stroke` attribute and for inline `style.borderColor`, so unit tests cannot
 * prove the dark theme actually lands a contrasting color. This script reads
 * the RESOLVED computed colors off the live DOM, measures WCAG contrast against
 * the resolved canvas / card surfaces, asserts, and only then writes PNGs.
 *
 * Assertions (both themes):
 *   - 5 lane paths render, each stroke resolves to a concrete rgb() (no `var(`)
 *   - lane dot borderColor resolves and matches its lane's stroke
 *   - every lane clears 4.5:1 against the resolved lane surface and card
 *   - light lane values are byte-identical to the frozen pre-token palette
 *   - dark lane values DIFFER from light (proves the pair is wired, not inert)
 *
 * Temporary tooling: delete after the shots are attached to LRM-1208.
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1208");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5208/";
const MIN_CONTRAST = 4.5;
const LANE_COUNT = 5;

/** Frozen light palette that predated the tokens (must not drift). */
const FROZEN_LIGHT = [
  "rgb(15, 118, 110)",
  "rgb(194, 65, 12)",
  "rgb(29, 78, 216)",
  "rgb(124, 58, 237)",
  "rgb(180, 83, 9)",
];

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});
const page = await browser.newPage({ viewport: { width: 420, height: 1000 } });

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

async function probe(theme) {
  await page.goto(`${base}?theme=${theme}`, { waitUntil: "networkidle" });
  await page.waitForSelector('[data-testid="research-git-list"]');
  await page.waitForTimeout(200);

  return page.evaluate(() => {
    const srgbToLinear = (c) => {
      const v = c / 255;
      return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
    };
    const canvas = document.createElement("canvas");
    canvas.width = 1;
    canvas.height = 1;
    const ctx = canvas.getContext("2d", { willReadFrequently: true });
    /**
     * Chromium serializes computed colors in their authored color space, so a
     * dark lane comes back as `oklch(0.78 0.12 175)`. Paint it once and read
     * the pixel to get the true sRGB the user actually sees.
     */
    const parse = (value) => {
      if (!value) return null;
      const direct = value.match(
        /rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)/,
      );
      if (direct) {
        return [Number(direct[1]), Number(direct[2]), Number(direct[3])];
      }
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = "#000000";
      ctx.fillStyle = value;
      if (ctx.fillStyle === "#000000" && !/^#0{3,8}$/.test(value.trim())) {
        return null;
      }
      ctx.fillRect(0, 0, 1, 1);
      const d = ctx.getImageData(0, 0, 1, 1).data;
      return [d[0], d[1], d[2]];
    };
    const asRgbString = (value) => {
      const rgb = parse(value);
      return rgb ? `rgb(${rgb[0]}, ${rgb[1]}, ${rgb[2]})` : null;
    };
    const lum = (rgb) =>
      0.2126 * srgbToLinear(rgb[0]) +
      0.7152 * srgbToLinear(rgb[1]) +
      0.0722 * srgbToLinear(rgb[2]);

    const list = document.querySelector(
      '[data-testid="research-git-list"]',
    );
    const listCs = getComputedStyle(list);
    const cardEl = document.querySelector(
      '[data-testid="research-git-list-card"]',
    );

    const paths = [...list.querySelectorAll("svg path")].map((p) => ({
      // Raw attribute keeps the authored value; computed style resolves the var.
      attr: p.getAttribute("stroke"),
      raw: getComputedStyle(p).stroke,
      resolved: asRgbString(getComputedStyle(p).stroke),
    }));

    // Lane dots: absolute span with border-2 + inline borderColor.
    const dots = [...list.querySelectorAll("span[style*='border-color']")].map(
      (d) => ({
        inline: d.style.borderColor,
        resolved: asRgbString(getComputedStyle(d).borderTopColor),
        fill: asRgbString(getComputedStyle(d).backgroundColor),
      }),
    );

    const laneVars = [];
    for (let i = 1; i <= 5; i += 1) {
      laneVars.push(
        listCs.getPropertyValue(`--research-lane-${i}`).trim(),
      );
    }

    return {
      isDark: document.documentElement.classList.contains("dark"),
      laneVars,
      paths,
      dots,
      surface: asRgbString(listCs.backgroundColor),
      surfaceLum: lum(parse(listCs.backgroundColor) ?? [255, 255, 255]),
      card: cardEl
        ? asRgbString(getComputedStyle(cardEl).backgroundColor)
        : null,
      cardLum: cardEl
        ? lum(
            parse(getComputedStyle(cardEl).backgroundColor) ?? [255, 255, 255],
          )
        : null,
      lums: paths.map((p) => lum(parse(p.raw) ?? [0, 0, 0])),
      dotLums: dots.map((d) => lum(parse(d.resolved) ?? [0, 0, 0])),
      cardCount: document.querySelectorAll(
        '[data-testid="research-git-list-card"]',
      ).length,
    };
  });
}

const cr = (a, b) => {
  const [hi, lo] = a >= b ? [a, b] : [b, a];
  return (hi + 0.05) / (lo + 0.05);
};

const summary = [];

for (const theme of ["light", "dark"]) {
  const r = await probe(theme);

  if (theme === "dark" && !r.isDark) fail("dark theme did not apply .dark");
  if (r.cardCount < 10) fail(`${theme}: only ${r.cardCount} cards rendered`);

  // The fixture forks 5 ways and then wraps, so lane 6 reuses lane-1: assert on
  // token coverage, not on path count.
  const seen = new Map();
  r.paths.forEach((p, i) => {
    const m = /^var\(--research-lane-(\d)\)$/.exec(p.attr ?? "");
    if (!m) {
      fail(
        `${theme}: path ${i} stroke attribute is "${p.attr}", expected a lane token ref`,
      );
    }
    if (!/^rgba?\(/.test(p.resolved)) {
      fail(
        `${theme}: lane token ${m[1]} did not resolve (got "${p.resolved}")`,
      );
    }
    const prev = seen.get(m[1]);
    if (prev && prev.resolved !== p.resolved) {
      fail(`${theme}: lane token ${m[1]} resolved inconsistently`);
    }
    seen.set(m[1], { resolved: p.resolved, lum: r.lums[i] });
  });

  for (let i = 1; i <= LANE_COUNT; i += 1) {
    if (!seen.has(String(i))) {
      fail(`${theme}: lane token ${i} never rendered — fixture lost coverage`);
    }
  }

  if (r.dots.length < LANE_COUNT) {
    fail(`${theme}: only ${r.dots.length} lane dots found`);
  }
  r.dots.forEach((d, i) => {
    if (!d.inline.startsWith("var(--research-lane-")) {
      fail(`${theme}: dot ${i} inline borderColor is "${d.inline}"`);
    }
    if (!/^rgba?\(/.test(d.resolved)) {
      fail(`${theme}: dot ${i} borderColor did not resolve`);
    }
  });

  const rows = [];
  for (let i = 1; i <= LANE_COUNT; i += 1) {
    const lane = seen.get(String(i));
    const vsSurface = cr(lane.lum, r.surfaceLum);
    const vsCard = cr(lane.lum, r.cardLum ?? r.surfaceLum);
    rows.push(
      `  lane ${i}: ${lane.resolved.padEnd(20)} vs canvas ${vsSurface.toFixed(2)} · vs card ${vsCard.toFixed(2)}`,
    );
    if (vsSurface < MIN_CONTRAST || vsCard < MIN_CONTRAST) {
      fail(
        `${theme}: lane ${i} contrast canvas ${vsSurface.toFixed(2)} / card ${vsCard.toFixed(2)} < ${MIN_CONTRAST}`,
      );
    }
    const frozen = FROZEN_LIGHT[i - 1];
    const resolved = lane.resolved.replace(/\s+/g, " ");
    if (theme === "light" && resolved !== frozen) {
      fail(
        `light: lane ${i} drifted from the frozen palette: ${resolved} != ${frozen}`,
      );
    }
    if (theme === "dark" && resolved === frozen) {
      fail(
        `dark: lane ${i} still renders the light value ${resolved} — the dark pair is inert`,
      );
    }
  }

  summary.push(
    `[${theme}] surface=${r.surface} card=${r.card} cards=${r.cardCount}`,
    `  tokens: ${r.laneVars.join(" | ")}`,
    ...rows,
  );

  await page.screenshot({
    path: resolve(outDir, `01-git-lanes-${theme}-420.png`),
    fullPage: false,
  });
  // Gutter-only crop: the lane lines and dots are ~72px wide, so a full-width
  // shot makes the color change hard to read at review size.
  await page.screenshot({
    path: resolve(outDir, `03-gutter-${theme}-420.png`),
    clip: { x: 0, y: 0, width: 96, height: 1000 },
  });
  await page.setViewportSize({ width: 375, height: 900 });
  await page.waitForTimeout(150);
  await page.screenshot({
    path: resolve(outDir, `02-git-lanes-${theme}-375-narrow.png`),
  });
  await page.setViewportSize({ width: 420, height: 1000 });
}

if (errors.length) fail(`console/page errors:\n${errors.join("\n")}`);

const text = summary.join("\n");
writeFileSync(resolve(outDir, "measurements.txt"), `${text}\n`);
console.log(text);
console.log(`\nPASS — shots + measurements in ${outDir}`);

await browser.close();
