/**
 * LRM-1369 — live Chromium contrast for the success wash label, plus a
 * zero-regression probe proving `--success` (presence dots / solid fills /
 * dividers) did not move when `--success-strong` was introduced.
 *
 * Usage: node scripts/lrm1369-gate-shots.mjs   (harness on :5369)
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1369");
mkdirSync(outDir, { recursive: true });
const base = process.env.HARNESS_URL ?? "http://localhost:5369/";
const MIN = 4.5;

// Frozen `--success` (= --ok) values that must survive the token addition.
// Custom properties keep their authored form in getComputedStyle, so accept
// either spelling of the frozen value.
const FROZEN_SUCCESS = {
  light: ["#007a5a", "rgb(0, 122, 90)"],
  dark: ["oklch(0.65 0.15 145)"],
};

const LABELS = ["label-5", "label-10", "label-15", "label-card", "label-muted"];
const PROBES = [
  "dot-success",
  "divider-success",
  "solid-success",
  "text-success-plain",
];

const KERNEL = `((labels, probes) => {
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
    const la = lum(a);
    const lb = lum(b);
    return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
  };
  const rgbStr = (c) =>
    "rgb(" + Math.round(c.r) + ", " + Math.round(c.g) + ", " + Math.round(c.b) + ")";
  const backdropOf = (el) => {
    let bg = { r: 255, g: 255, b: 255, a: 1 };
    const stack = [];
    let n = el;
    while (n && n !== document.documentElement) {
      const cs = getComputedStyle(n);
      const p = parse(cs.backgroundColor);
      if (p && p.a > 0) stack.push(p);
      n = n.parentElement;
    }
    const rootBg =
      parse(getComputedStyle(document.documentElement).backgroundColor) || bg;
    bg = rootBg.a >= 1 ? rootBg : over(rootBg, bg);
    for (let i = stack.length - 1; i >= 0; i--) bg = over(stack[i], bg);
    return bg;
  };
  const measure = (id) => {
    const sel = '[data-testid="' + id + '"]';
    const el = document.querySelector(sel);
    if (!el) return { sel, error: "missing " + sel };
    const cs = getComputedStyle(el);
    const fg = parse(cs.color);
    const bg = backdropOf(el);
    const solid = over({ ...fg, a: 1 }, bg);
    return {
      sel,
      color: cs.color,
      alpha: fg ? fg.a : null,
      contrast: Number(ratio(solid, bg).toFixed(2)),
      bg: rgbStr(bg),
    };
  };
  const probe = (id) => {
    const sel = '[data-testid="' + id + '"]';
    const el = document.querySelector(sel);
    if (!el) return { sel, error: "missing " + sel };
    const cs = getComputedStyle(el);
    const fill = parse(cs.backgroundColor);
    return {
      sel,
      backgroundColor: cs.backgroundColor,
      color: cs.color,
      fillRgb: fill && fill.a > 0 ? rgbStr(fill) : null,
    };
  };
  return {
    labels: labels.map(measure),
    probes: probes.map(probe),
    tokens: {
      success: getComputedStyle(document.documentElement)
        .getPropertyValue("--success")
        .trim(),
      successStrong: getComputedStyle(document.documentElement)
        .getPropertyValue("--success-strong")
        .trim(),
    },
  };
})(${JSON.stringify(LABELS)}, ${JSON.stringify(PROBES)})`;

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});
const report = {};
const fails = [];

for (const phase of ["before", "after"]) {
  for (const theme of ["light", "dark"]) {
    const page = await browser.newPage({
      viewport: { width: 520, height: 520 },
    });
    await page.goto(`${base}?theme=${theme}&before=${phase === "before" ? 1 : 0}`, {
      waitUntil: "networkidle",
    });
    await page.waitForSelector('[data-testid="lrm1369-surface"]');
    await page.waitForTimeout(200);
    const data = await page.evaluate(KERNEL);
    report[`${phase}-${theme}`] = data;

    if (phase === "after") {
      for (const site of data.labels) {
        if (site.error) fails.push(`${theme}: ${site.error}`);
        else if (site.contrast < MIN) {
          fails.push(
            `AFTER ${theme}/${site.sel}: ${site.contrast} < ${MIN} (${site.color} on ${site.bg})`,
          );
        }
        if (site.alpha !== null && site.alpha < 1) {
          fails.push(`AFTER ${theme}/${site.sel}: label alpha ${site.alpha} < 1`);
        }
      }
      // Zero regression: --success must still resolve to the frozen value and
      // dots/dividers/solid fills must still paint with it.
      const dot = data.probes.find((p) => p.sel.includes("dot-success"));
      const divider = data.probes.find((p) => p.sel.includes("divider-success"));
      const solid = data.probes.find((p) => p.sel.includes("solid-success"));
      const beforeDot = report[`before-${theme}`]?.probes?.find((p) =>
        p.sel.includes("dot-success"),
      );
      for (const [name, probe] of [
        ["dot", dot],
        ["divider", divider],
        ["solid", solid],
      ]) {
        if (!probe || probe.error) {
          fails.push(`${theme}: missing ${name} probe`);
          continue;
        }
        if (probe.fillRgb !== dot?.fillRgb) {
          fails.push(
            `${theme}: ${name} fill ${probe.fillRgb} drifted from dot ${dot?.fillRgb}`,
          );
        }
      }
      if (beforeDot && dot && beforeDot.fillRgb !== dot.fillRgb) {
        fails.push(
          `${theme}: presence dot regressed ${beforeDot.fillRgb} -> ${dot.fillRgb}`,
        );
      }
      if (!FROZEN_SUCCESS[theme].includes(data.tokens.success)) {
        fails.push(
          `${theme}: --success moved to "${data.tokens.success}" (expected one of ${FROZEN_SUCCESS[theme].join(" | ")})`,
        );
      }
    }

    writeFileSync(
      resolve(outDir, `${phase}-${theme}.png`),
      await page.screenshot({ fullPage: true }),
    );
    await page.close();
  }
}

// The baseline must actually fail, otherwise the gate proves nothing.
const beforeFails = ["light", "dark"].flatMap((theme) =>
  (report[`before-${theme}`]?.labels ?? [])
    .filter((site) => !site.error && site.contrast < MIN)
    .map((site) => `${theme}/${site.sel}=${site.contrast}`),
);
if (beforeFails.length === 0) {
  fails.push("BEFORE baseline did not reproduce any AA failure");
}

writeFileSync(
  resolve(outDir, "measurements.json"),
  JSON.stringify({ min: MIN, beforeFails, report }, null, 2),
);
await browser.close();

if (fails.length) {
  console.error(fails.join("\n"));
  process.exit(1);
}
console.log(
  JSON.stringify({ beforeFails, after: report["after-light"].labels }, null, 2),
);
