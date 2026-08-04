/**
 * LRM-1304 gate shots — message-row reading geometry in a real browser.
 *
 * Why a browser and not jsdom: every number this design gate has to lock is a
 * layout fact behind a media query — the permanent `(pointer: fine)` right
 * gutter, the real width of the 5-key overlay action bar, and whether that bar
 * intersects real body text line boxes. jsdom applies no media queries and
 * produces no line boxes, so only Chromium can answer.
 *
 * Live-DOM assertions run first; PNGs are written only after they pass.
 *
 * Usage: node scripts/lrm1304-gate-shots.mjs        (harness on :5304)
 *
 * Temporary tooling: delete after the shots are attached to LRM-1304.
 */
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1304");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5304/";
const COMPONENT = "packages/views/channels/components/channel-message-bubble.tsx";
const HARNESS = "scripts/lrm1304-harness/main.tsx";

/* ---------------------------------------------------------------- *
 * 0. The harness may not invent BEFORE markup: every verbatim class
 *    string it measures must exist in the real component source.
 * ---------------------------------------------------------------- */
const componentSrc = readFileSync(resolve(root, COMPONENT), "utf8");
const harnessSrc = readFileSync(resolve(root, HARNESS), "utf8");
const errors = [];

const verbatim = (label, re) => {
  const m = harnessSrc.match(re);
  if (!m) {
    errors.push(`harness: cannot read ${label}`);
    return;
  }
  if (!componentSrc.includes(m[1])) {
    errors.push(`drift: harness ${label} is not verbatim in ${COMPONENT}`);
  }
};
verbatim("ROW_CLASS", /export const ROW_CLASS =\s*"([^"]+)"/);
verbatim("MESSAGE_SHELL_CLASS", /export const MESSAGE_SHELL_CLASS =\s*"([^"]+)"/);
verbatim("SHELL_GUTTER_CLASS", /export const SHELL_GUTTER_CLASS = "([^"]+)"/);
verbatim("BAR_CLASS", /export const BAR_CLASS =\s*"([^"]+)"/);
verbatim("BODY_CLASS", /export const BODY_CLASS =\s*"([^"]+)"/);
verbatim("AUTHOR_ROW_CLASS", /export const AUTHOR_ROW_CLASS =\s*"([^"]+)"/);
if (errors.length) {
  console.error(errors.join("\n"));
  process.exit(1);
}

const viewports = {
  1440: { width: 1440, height: 900 },
  700: { width: 700, height: 900 },
  360: { width: 360, height: 780 },
};

const frames = [];
for (const variant of ["before", "spec"]) {
  for (const vp of ["1440", "700", "360"]) {
    for (const theme of ["light", "dark"]) {
      if (vp !== "1440" && theme === "dark") continue; // dark only needed at 1440 + 360
      frames.push({ variant, vp, theme });
    }
  }
  frames.push({ variant, vp: "360", theme: "dark" });
}

const HOVER_ROWS = ["lead-long", "cont-long", "cont-quote", "cont-replies"];

const browser = await chromium.launch({ executablePath: process.env.CHROME_BIN || undefined });
const report = [];
const ignorable = (t) => t.includes("favicon.ico") || t.includes("/api/");

for (const frame of frames) {
  const page = await browser.newPage({ viewport: viewports[frame.vp], hasTouch: false });
  page.on("pageerror", (e) => errors.push(`${frame.variant}/${frame.vp}: ${e}`));
  page.on("console", (m) => {
    if (m.type() === "error" && !ignorable(m.location().url + m.text())) {
      errors.push(`${frame.variant}/${frame.vp}: ${m.text()}`);
    }
  });

  const url = `${base}?variant=${frame.variant}&theme=${frame.theme}`;
  await page.goto(url, { waitUntil: "networkidle" });
  await page.waitForSelector('[data-testid="lrm1304-surface"]');

  const pointerFine = await page.evaluate(() => matchMedia("(pointer: fine)").matches);
  if (!pointerFine) errors.push(`${frame.variant}/${frame.vp}: harness needs (pointer: fine)`);

  const measured = { ...frame, pointerFine, rows: {} };

  for (const rowId of HOVER_ROWS) {
    const row = page.locator(`[data-testid="row-${rowId}"]`);
    await row.hover({ position: { x: 20, y: 8 } });
    await page.waitForTimeout(180); // opacity transition settles

    measured.rows[rowId] = await page.evaluate((id) => {
      const row = document.querySelector(`[data-testid="row-${id}"]`);
      const shell = row.querySelector('[data-testid="message-shell"]');
      const body = row.querySelector('[data-testid="message-body"]');
      const bar = row.querySelector('[data-testid="message-action-bar"]');
      const text = row.querySelector('[data-testid="message-text"]');
      const cs = getComputedStyle(shell);
      const barBox = bar.getBoundingClientRect();
      const barVisible = Number(getComputedStyle(bar).opacity) > 0.5;

      // Real line boxes of every text node in the body (paragraph, quote card,
      // thread reply preview) — not just the main paragraph.
      //
      // `getClientRects()` on a text node inside a `truncate` (overflow:hidden)
      // ancestor returns the UNCLIPPED layout extent, which would report a
      // clipped-away glyph run as sitting under the bar. Clamp every rect by its
      // clipping ancestors so only painted text counts.
      const clipRect = (node) => {
        let box = null;
        for (let el = node.parentElement; el && el !== document.body; el = el.parentElement) {
          const o = getComputedStyle(el);
          if (o.overflow === "hidden" || o.overflow === "clip" || o.overflowX === "hidden") {
            const r = el.getBoundingClientRect();
            box = box
              ? {
                  left: Math.max(box.left, r.left),
                  right: Math.min(box.right, r.right),
                  top: Math.max(box.top, r.top),
                  bottom: Math.min(box.bottom, r.bottom),
                }
              : { left: r.left, right: r.right, top: r.top, bottom: r.bottom };
          }
        }
        return box;
      };
      const walker = document.createTreeWalker(body, NodeFilter.SHOW_TEXT);
      const lines = [];
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        if (!n.nodeValue || !n.nodeValue.trim()) continue;
        const clip = clipRect(n);
        const r = document.createRange();
        r.selectNodeContents(n);
        for (const rect of r.getClientRects()) {
          const box = clip
            ? {
                left: Math.max(rect.left, clip.left),
                right: Math.min(rect.right, clip.right),
                top: Math.max(rect.top, clip.top),
                bottom: Math.min(rect.bottom, clip.bottom),
              }
            : rect;
          if (box.right - box.left > 1 && box.bottom - box.top > 1) lines.push(box);
        }
      }
      const textRange = document.createRange();
      textRange.selectNodeContents(text);
      const textLines = [...textRange.getClientRects()].filter(
        (r) => r.width > 1 && r.height > 1,
      );
      const overlapArea = lines.reduce((sum, r) => {
        const w = Math.min(r.right, barBox.right) - Math.max(r.left, barBox.left);
        const h = Math.min(r.bottom, barBox.bottom) - Math.max(r.top, barBox.top);
        return sum + (w > 0.5 && h > 0.5 ? w * h : 0);
      }, 0);

      const truncated = [...row.querySelectorAll(".truncate")].map((el) => ({
        scrollW: Math.round(el.scrollWidth),
        clientW: Math.round(el.clientWidth),
        clipped: el.scrollWidth - el.clientWidth > 1,
      }));

      return {
        shellPaddingRight: cs.paddingRight,
        shellBorder: {
          top: cs.borderTopWidth,
          right: cs.borderRightWidth,
          bottom: cs.borderBottomWidth,
          left: cs.borderLeftWidth,
        },
        bodyWidth: Math.round(body.getBoundingClientRect().width),
        longestLine: Math.round(Math.max(0, ...textLines.map((r) => r.width))),
        lineCount: textLines.length,
        bar: {
          w: Math.round(barBox.width),
          h: Math.round(barBox.height),
          visible: barVisible,
          top: Math.round(barBox.top - row.getBoundingClientRect().top),
        },
        overlapArea: Math.round(overlapArea),
        truncated,
      };
    }, rowId);
  }

  /* ---------------- gate assertions ---------------- */
  const tag = `${frame.variant}/${frame.vp}/${frame.theme}`;
  // SPEC drops the hover bar below 640px (see harness SPEC_BAR_CLASS).
  const specNarrowFallback = frame.variant === "spec" && Number(frame.vp) < 640;
  for (const [rowId, m] of Object.entries(measured.rows)) {
    if (specNarrowFallback) {
      if (m.bar.visible) errors.push(`${tag}/${rowId}: SPEC bar must not paint below 640px`);
    } else if (!m.bar.visible) {
      errors.push(`${tag}/${rowId}: action bar not visible on hover`);
    }
    if (frame.variant === "spec") {
      if (m.overlapArea > 0) {
        errors.push(`${tag}/${rowId}: SPEC bar still covers body text (${m.overlapArea}px²)`);
      }
      if (parseFloat(m.shellPaddingRight) > 4.5) {
        errors.push(`${tag}/${rowId}: SPEC shell still reserves ${m.shellPaddingRight}`);
      }
      const anyBorder = Object.values(m.shellBorder).some((v) => parseFloat(v) > 0);
      if (anyBorder) errors.push(`${tag}/${rowId}: SPEC shell still paints a border`);
    }
  }

  report.push(measured);

  const file = `lrm1304-${frame.variant}-${frame.vp}-${frame.theme}.png`;
  await page.screenshot({ path: resolve(outDir, file), fullPage: false });
  await page.close();
}

await browser.close();

writeFileSync(resolve(outDir, "measurements.json"), JSON.stringify(report, null, 2));

/* ---------------- console summary ---------------- */
const pick = (variant, vp, row) =>
  report.find((r) => r.variant === variant && r.vp === vp && r.theme === "light")?.rows[row];
for (const vp of ["1440", "700", "360"]) {
  for (const row of HOVER_ROWS) {
    const b = pick("before", vp, row);
    const s = pick("spec", vp, row);
    if (!b || !s) continue;
    console.log(
      `vp ${vp} ${row.padEnd(13)} body ${String(b.bodyWidth).padStart(4)} → ${String(s.bodyWidth).padStart(4)}` +
        `  longest line ${String(b.longestLine).padStart(4)} → ${String(s.longestLine).padStart(4)}` +
        `  lines ${b.lineCount}→${s.lineCount}` +
        `  bar ${b.bar.w}×${b.bar.h} @top${b.bar.top}` +
        `  overlap ${b.overlapArea} → ${s.overlapArea}` +
        `  shell pr ${b.shellPaddingRight}→${s.shellPaddingRight}`,
    );
  }
}

if (errors.length) {
  console.error("\nFAIL\n" + [...new Set(errors)].join("\n"));
  process.exit(1);
}
console.log(`\nOK — ${frames.length} frames + measurements.json → ${outDir}`);
