/**
 * LRM-1353 — live Chromium contrast for text-warning on bg-warning/* wash.
 * Usage: node scripts/lrm1353-gate-shots.mjs   (harness on :5353)
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1353");
mkdirSync(outDir, { recursive: true });
const base = process.env.HARNESS_URL ?? "http://localhost:5353/";
const MIN = 4.5;

const KERNEL = `(() => {
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
  const measure = (sel) => {
    const el = document.querySelector(sel);
    if (!el) return { error: "missing " + sel };
    const cs = getComputedStyle(el);
    const fg = parse(cs.color);
    const bg = backdropOf(el);
    const solid = over({ ...fg, a: 1 }, bg);
    return {
      sel,
      color: cs.color,
      contrast: Number(ratio(solid, bg).toFixed(2)),
      bg:
        "rgb(" +
        Math.round(bg.r) +
        ", " +
        Math.round(bg.g) +
        ", " +
        Math.round(bg.b) +
        ")",
    };
  };
  return ["label-5", "label-10", "label-15", "label-card"].map((id) =>
    measure('[data-testid="' + id + '"]'),
  );
})()`;

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});
const report = {};
const fails = [];
for (const theme of ["light", "dark"]) {
  const page = await browser.newPage({ viewport: { width: 480, height: 420 } });
  await page.goto(`${base}?theme=${theme}`, { waitUntil: "networkidle" });
  await page.waitForSelector('[data-testid="lrm1353-surface"]');
  await page.waitForTimeout(200);
  await page.evaluate(() =>
    document.querySelectorAll("nextjs-portal").forEach((el) => el.remove()),
  );
  const sites = await page.evaluate(KERNEL);
  report[theme] = sites;
  for (const s of sites) {
    if (s.error) fails.push(`${theme}: ${s.error}`);
    else if (s.contrast < MIN) {
      fails.push(
        `${theme}/${s.sel}: ${s.contrast} < ${MIN} (${s.color} on ${s.bg})`,
      );
    }
  }
  writeFileSync(
    resolve(outDir, `after-${theme}.png`),
    await page.screenshot({ fullPage: true }),
  );
  await page.close();
}
writeFileSync(resolve(outDir, "contrast.json"), JSON.stringify(report, null, 2));
await browser.close();
if (fails.length) {
  console.error(fails.join("\n"));
  process.exit(1);
}
console.log(JSON.stringify(report, null, 2));
