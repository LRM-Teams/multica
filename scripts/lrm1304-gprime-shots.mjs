/**
 * LRM-1304 G′ gate shots — grouping without any shell border, in a real browser.
 *
 * Frank locked ①: the message shell and the joined-group shell lose every
 * visible edge. That deletes two things at once — the group silhouette and the
 * hover signal (`border-line` → `border-line-strong`). This script measures
 * whether the replacements actually carry them:
 *
 *   1. rhythm      — shell-to-shell whitespace across a group boundary vs inside
 *                    a group (the silhouette replacement)
 *   2. hover wash  — does the row surface actually change on hover (G′) or not
 *                    (G″), measured as a computed colour delta
 *   3. no edge     — every shell segment reports 0px border and 0px radius
 *   4. no regress  — body still eats the full row width and the overlay bar
 *                    still never lands on body text (SPEC / LRM-1331 invariants)
 *   5. self-mention — the one row wash we already ship must still read as a block
 *                    with no edge under it
 *
 * Live-DOM assertions run first; PNGs are written only after they pass.
 *
 * Usage: node scripts/lrm1304-gprime-shots.mjs      (harness on :5304)
 *
 * Temporary tooling: delete after the shots are attached to LRM-1304.
 */
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1304-gprime");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5304/";
const COMPONENT = "packages/views/channels/components/channel-message-bubble.tsx";
const MENTION_TOKEN = "packages/views/common/mention-token.ts";
const HARNESS = "scripts/lrm1304-harness/main.tsx";
const LIST = "packages/views/channels/components/channel-message-list.tsx";

/* ---------------------------------------------------------------- *
 * 0. Drift gate: the harness may not invent BEFORE markup.
 * ---------------------------------------------------------------- */
const componentSrc = readFileSync(resolve(root, COMPONENT), "utf8");
const mentionSrc = readFileSync(resolve(root, MENTION_TOKEN), "utf8");
const listSrc = readFileSync(resolve(root, LIST), "utf8");
const harnessSrc = readFileSync(resolve(root, HARNESS), "utf8");
const errors = [];

const verbatim = (label, re, src, srcLabel) => {
  const m = harnessSrc.match(re);
  if (!m) {
    errors.push(`harness: cannot read ${label}`);
    return;
  }
  if (!src.includes(m[1])) errors.push(`drift: harness ${label} is not verbatim in ${srcLabel}`);
};
verbatim("ROW_CLASS", /export const ROW_CLASS =\s*"([^"]+)"/, componentSrc, COMPONENT);
verbatim("MESSAGE_SHELL_CLASS", /export const MESSAGE_SHELL_CLASS =\s*"([^"]+)"/, componentSrc, COMPONENT);
verbatim("SHELL_GUTTER_CLASS", /export const SHELL_GUTTER_CLASS = "([^"]+)"/, componentSrc, COMPONENT);
verbatim("BAR_CLASS", /export const BAR_CLASS =\s*"([^"]+)"/, componentSrc, COMPONENT);
verbatim("BODY_CLASS", /export const BODY_CLASS =\s*"([^"]+)"/, componentSrc, COMPONENT);
verbatim("AUTHOR_ROW_CLASS", /export const AUTHOR_ROW_CLASS =\s*"([^"]+)"/, componentSrc, COMPONENT);
verbatim("SELF_MENTION_ROW_CLASS", /export const SELF_MENTION_ROW_CLASS =\s*"([^"]+)"/, mentionSrc, MENTION_TOKEN);
// The date divider is the one horizontal rule G′ keeps — assert we copied the real one.
for (const frag of ['data-testid="date-divider"', 'h-px min-w-4 flex-1 bg-border/60']) {
  if (!listSrc.includes(frag)) errors.push(`drift: date divider fragment missing in ${LIST}: ${frag}`);
  if (!harnessSrc.includes(frag)) errors.push(`harness: date divider fragment not measured: ${frag}`);
}
// The hover wash must be existing product language, not a new invention.
const washPrecedents = [
  "packages/views/channels/components/channel-members-list.tsx",
  "packages/views/channels/components/channel-add-people-dialog.tsx",
  "packages/views/settings/components/members-tab.tsx",
];
for (const file of washPrecedents) {
  if (!readFileSync(resolve(root, file), "utf8").includes("hover:bg-hover")) {
    errors.push(`precedent lost: ${file} no longer uses hover:bg-hover`);
  }
}
// G′ only swaps the row's single transition-duration utility; both the shipped
// value and the derivation have to be real, or the timing evidence is fiction.
if (!componentSrc.includes("transition-colors duration-1000")) {
  errors.push(`drift: ${COMPONENT} no longer ships transition-colors duration-1000 on the row`);
}
if (!harnessSrc.includes('ROW_CLASS.replace("duration-1000", "duration-100")')) {
  errors.push("harness: G′ row class must be derived from ROW_CLASS by swapping the duration");
}
if (errors.length) {
  console.error(errors.join("\n"));
  process.exit(1);
}

const viewports = {
  1440: { width: 1440, height: 980 },
  700: { width: 700, height: 980 },
  360: { width: 360, height: 900 },
};

const VARIANTS = ["before", "gprime", "gsecond"];
const frames = [];
for (const variant of VARIANTS) {
  for (const vp of ["1440", "360"]) {
    for (const theme of ["light", "dark"]) frames.push({ variant, vp, theme });
  }
}
frames.push({ variant: "before", vp: "700", theme: "light" });
frames.push({ variant: "gprime", vp: "700", theme: "light" });
// Control: G′'s wash riding the shipped 1s row transition (timing evidence only).
frames.push({ variant: "gprimeslow", vp: "1440", theme: "light" });

const ROW_IDS = [
  "lead-long",
  "cont-long",
  "cont-quote",
  "cont-replies",
  "lead-short",
  "lead-day2",
  "cont-day2",
  "lead-mention",
];
/** Rows we hover-probe (bar + wash). */
const HOVER_ROWS = ["lead-long", "cont-long", "cont-replies", "lead-short"];
/** The row we leave hovered for the screenshot, so the frame shows the real state. */
const SHOT_HOVER_ROW = "cont-long";

const browser = await chromium.launch({ executablePath: process.env.CHROME_BIN || undefined });
const report = [];
const ignorable = (t) => t.includes("favicon.ico") || t.includes("/api/");

/** Alpha of a computed colour, for both `rgba(...)` and `oklab(... / a)` forms.
 *  A fully opaque colour reports no alpha component → 1. */
function alphaOf(color) {
  const oklab = color.match(/\/\s*([\d.]+)\s*\)/);
  if (oklab) return parseFloat(oklab[1]);
  const rgba = color.match(/^rgba?\(([^)]+)\)$/);
  if (rgba) {
    const parts = rgba[1].split(",").map((v) => parseFloat(v));
    return parts.length > 3 ? parts[3] : 1;
  }
  return 1;
}

for (const frame of frames) {
  const page = await browser.newPage({ viewport: viewports[frame.vp], hasTouch: false });
  page.on("pageerror", (e) => errors.push(`${frame.variant}/${frame.vp}: ${e}`));
  page.on("console", (m) => {
    if (m.type() === "error" && !ignorable(m.location().url + m.text())) {
      errors.push(`${frame.variant}/${frame.vp}: ${m.text()}`);
    }
  });

  await page.goto(`${base}?variant=${frame.variant}&theme=${frame.theme}`, {
    waitUntil: "networkidle",
  });
  await page.waitForSelector('[data-testid="lrm1304-surface"]');
  if (!(await page.evaluate(() => matchMedia("(pointer: fine)").matches))) {
    errors.push(`${frame.variant}/${frame.vp}: harness needs (pointer: fine)`);
  }

  /* ---- resting geometry, mouse parked off the message stack ---- */
  await page.mouse.move(5, 5);
  await page.waitForTimeout(120);

  const measured = await page.evaluate((ids) => {
    const px = (v) => Math.round(parseFloat(v) * 100) / 100;
    const rows = {};
    for (const id of ids) {
      const row = document.querySelector(`[data-testid="row-${id}"]`);
      const shell = row.querySelector('[data-testid="message-shell"]');
      const body = row.querySelector('[data-testid="message-body"]');
      const rs = getComputedStyle(row);
      const ss = getComputedStyle(shell);
      const shellBox = shell.getBoundingClientRect();
      rows[id] = {
        group: Number(row.dataset.group),
        compact: row.dataset.compact === "true",
        rowBgRest: rs.backgroundColor,
        shellPaddingRight: px(ss.paddingRight),
        shellBorder: [ss.borderTopWidth, ss.borderRightWidth, ss.borderBottomWidth, ss.borderLeftWidth].map(px),
        shellRadius: [ss.borderTopLeftRadius, ss.borderTopRightRadius, ss.borderBottomRightRadius, ss.borderBottomLeftRadius].map(px),
        shellTop: Math.round(shellBox.top * 100) / 100,
        shellBottom: Math.round(shellBox.bottom * 100) / 100,
        bodyWidth: Math.round(body.getBoundingClientRect().width),
      };
    }
    const surfaceBg = getComputedStyle(document.querySelector('[data-testid="lrm1304-surface"]')).backgroundColor;
    const divider = document.querySelector('[data-testid="date-divider"]');
    const dividerRules = divider
      ? [...divider.querySelectorAll("div")].map((d) => px(getComputedStyle(d).height))
      : null;
    // Any element that still paints a visible edge inside a message row.
    const strayEdges = [];
    for (const id of ids) {
      const row = document.querySelector(`[data-testid="row-${id}"]`);
      for (const el of [row, ...row.querySelectorAll("*")]) {
        const cs = getComputedStyle(el);
        const w = [cs.borderTopWidth, cs.borderRightWidth, cs.borderBottomWidth, cs.borderLeftWidth].map(px);
        if (w.some((v) => v > 0) && cs.borderTopStyle !== "none") {
          strayEdges.push({
            row: id,
            testid: el.dataset?.testid ?? el.tagName.toLowerCase(),
            widths: w,
            cls: (el.className || "").toString().slice(0, 60),
          });
        }
      }
    }
    return { rows, surfaceBg, dividerRules, strayEdges };
  }, ROW_IDS);

  /* ---- hover probes: wash delta + ramp timing + bar overlap ---- */
  const hover = {};
  for (const rowId of HOVER_ROWS) {
    const row = page.locator(`[data-testid="row-${rowId}"]`);
    const bgOf = () =>
      page.evaluate(
        (id) => getComputedStyle(document.querySelector(`[data-testid="row-${id}"]`)).backgroundColor,
        rowId,
      );
    await page.mouse.move(5, 5);
    await page.waitForTimeout(1200); // let any previous wash fully unwind
    await row.hover({ position: { x: 20, y: 6 } });
    await page.waitForTimeout(110);
    const bgAt110 = await bgOf();
    await page.waitForTimeout(150);
    const bgAt260 = await bgOf();
    await page.waitForTimeout(950);
    hover[rowId] = {
      bgAt110,
      bgAt260,
      ...(await page.evaluate((id) => {
      const row = document.querySelector(`[data-testid="row-${id}"]`);
      const body = row.querySelector('[data-testid="message-body"]');
      const bar = row.querySelector('[data-testid="message-action-bar"]');
      const barBox = bar.getBoundingClientRect();
      const barVisible = Number(getComputedStyle(bar).opacity) > 0.5;
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
      let overlapArea = 0;
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
          if (box.right - box.left <= 1 || box.bottom - box.top <= 1) continue;
          const w = Math.min(box.right, barBox.right) - Math.max(box.left, barBox.left);
          const h = Math.min(box.bottom, barBox.bottom) - Math.max(box.top, barBox.top);
          if (w > 0.5 && h > 0.5) overlapArea += w * h;
        }
      }
      const ss = getComputedStyle(row.querySelector('[data-testid="message-shell"]'));
      return {
        rowBgHover: getComputedStyle(row).backgroundColor,
        shellBorderHover: [ss.borderTopWidth, ss.borderRightWidth, ss.borderBottomWidth, ss.borderLeftWidth].map(
          (v) => Math.round(parseFloat(v) * 100) / 100,
        ),
        shellBorderColorHover: ss.borderTopColor,
        barVisible,
        barW: Math.round(barBox.width),
        barH: Math.round(barBox.height),
        overlapArea: Math.round(overlapArea),
        gutterTimeOpacity: Number(
          getComputedStyle(row.querySelector('[data-testid="message-gutter-time"]') ?? row).opacity,
        ),
      };
    }, rowId)),
    };
  }

  /* ---- derived: shell-to-shell whitespace, tagged by boundary ---- */
  const gaps = [];
  for (let i = 1; i < ROW_IDS.length; i += 1) {
    const prev = measured.rows[ROW_IDS[i - 1]];
    const next = measured.rows[ROW_IDS[i]];
    // The date divider inserts a real boundary of its own; skip that pair.
    if (ROW_IDS[i] === "lead-day2") continue;
    gaps.push({
      from: ROW_IDS[i - 1],
      to: ROW_IDS[i],
      crossGroup: prev.group !== next.group,
      gap: Math.round((next.shellTop - prev.shellBottom) * 100) / 100,
    });
  }

  const entry = { ...frame, ...measured, hover, gaps };
  const crossAll = gaps.filter((g) => g.crossGroup).map((g) => g.gap);
  const intraAll = gaps.filter((g) => !g.crossGroup).map((g) => g.gap);
  entry.rhythm = {
    cross: crossAll,
    intra: intraAll,
    ratio: Math.round((Math.min(...crossAll) / Math.max(0.5, Math.max(...intraAll))) * 10) / 10,
  };
  report.push(entry);

  /* ---------------- gate assertions ---------------- */
  const tag = `${frame.variant}/${frame.vp}/${frame.theme}`;
  const borderless = frame.variant !== "before";
  const narrow = Number(frame.vp) < 640;

  if (!measured.dividerRules || measured.dividerRules.filter((h) => h === 1).length < 2) {
    errors.push(`${tag}: date divider must keep its two 1px rules, got ${measured.dividerRules}`);
  }

  if (borderless) {
    for (const [id, m] of Object.entries(measured.rows)) {
      if (m.shellBorder.some((v) => v > 0)) {
        errors.push(`${tag}/${id}: shell still paints an edge ${m.shellBorder}`);
      }
      if (m.shellRadius.some((v) => v > 0)) {
        errors.push(`${tag}/${id}: shell still rounds a corner ${m.shellRadius} (card look)`);
      }
      if (m.shellPaddingRight > 4.5) {
        errors.push(`${tag}/${id}: shell still reserves ${m.shellPaddingRight}px on the right`);
      }
    }
    // Only allowed remaining edges inside a row: the overlay bar, the quote card,
    // and the dark self-mention left bar.
    const allowed = new Set(["message-action-bar", "message-quote-card"]);
    for (const e of measured.strayEdges) {
      const selfMentionBar = e.row === "lead-mention" && e.widths[3] === 2;
      if (!allowed.has(e.testid) && !selfMentionBar) {
        errors.push(`${tag}: unexpected edge on ${e.row}/${e.testid} ${JSON.stringify(e.widths)}`);
      }
    }
    for (const g of entry.gaps) {
      if (g.crossGroup && g.gap < 8) {
        errors.push(`${tag}: group boundary ${g.from}→${g.to} only ${g.gap}px (need ≥ 8)`);
      }
      if (!g.crossGroup && g.gap > 2.5) {
        errors.push(`${tag}: intra-group ${g.from}→${g.to} opened to ${g.gap}px (need ≤ 2)`);
      }
    }
    const cross = entry.gaps.filter((g) => g.crossGroup).map((g) => g.gap);
    const intra = entry.gaps.filter((g) => !g.crossGroup).map((g) => g.gap);
    const ratio = Math.min(...cross) / Math.max(1, Math.max(...intra));
    if (ratio < 4) errors.push(`${tag}: rhythm contrast only ${ratio.toFixed(1)}:1 (need ≥ 4:1)`);

    // Self-mention wash must still separate from the pane surface with no edge.
    if (measured.rows["lead-mention"].rowBgRest === measured.surfaceBg) {
      errors.push(`${tag}: self-mention wash vanished (row bg == surface bg)`);
    }
  } else {
    for (const [id, m] of Object.entries(measured.rows)) {
      if (id !== "lead-mention" && !m.shellBorder.some((v) => v > 0)) {
        errors.push(`${tag}/${id}: BEFORE frame should still show the shipped edge`);
      }
    }
  }

  for (const [id, h] of Object.entries(hover)) {
    if (narrow) {
      if (borderless && h.barVisible) errors.push(`${tag}/${id}: bar must not paint below 640px`);
    } else if (!h.barVisible) {
      errors.push(`${tag}/${id}: action bar not visible on hover`);
    }
    if (borderless && !narrow && h.overlapArea > 0) {
      errors.push(`${tag}/${id}: bar covers body text (${h.overlapArea}px²)`);
    }
    const rest = measured.rows[id].rowBgRest;
    const changed = h.rowBgHover !== rest;
    if (frame.variant === "gprime" && !changed) {
      errors.push(`${tag}/${id}: G′ hover produced no surface change (${rest})`);
    }
    if (frame.variant === "gsecond" && changed) {
      errors.push(`${tag}/${id}: G″ must not wash on hover (${rest} → ${h.rowBgHover})`);
    }
    // Ramp: the wash has to be there when the pointer is, not a second later.
    const settled = alphaOf(h.rowBgHover);
    h.ramp110 = settled > 0 ? Math.round((alphaOf(h.bgAt110) / settled) * 1000) / 1000 : null;
    h.ramp260 = settled > 0 ? Math.round((alphaOf(h.bgAt260) / settled) * 1000) / 1000 : null;
    if (frame.variant === "gprime" && h.ramp110 !== null && h.ramp110 < 0.9) {
      errors.push(`${tag}/${id}: wash only ${(h.ramp110 * 100).toFixed(0)}% built at 110ms`);
    }
  }

  const file = `lrm1304-${frame.variant}-${frame.vp}-${frame.theme}.png`;
  const shotRow = page.locator(`[data-testid="row-${SHOT_HOVER_ROW}"]`);
  await shotRow.hover({ position: { x: 20, y: 6 } });
  await page.waitForTimeout(220);
  await page.screenshot({ path: resolve(outDir, file), fullPage: narrow });
  await page.close();
}

await browser.close();
writeFileSync(resolve(outDir, "measurements.json"), JSON.stringify(report, null, 2));

/* ---------------- console summary ---------------- */
const pick = (variant, vp, theme) =>
  report.find((r) => r.variant === variant && r.vp === vp && r.theme === theme);
for (const vp of ["1440", "360"]) {
  for (const theme of ["light", "dark"]) {
    for (const variant of VARIANTS) {
      const r = pick(variant, vp, theme);
      if (!r) continue;
      const b = r.rows["lead-long"];
      const h = r.hover["cont-long"];
      console.log(
        `${vp}/${theme}/${variant.padEnd(7)} border ${b.shellBorder.join("/")} radius ${b.shellRadius[0]}` +
          `  body ${String(b.bodyWidth).padStart(4)}` +
          `  gaps cross ${r.rhythm.cross.join(",")} intra ${r.rhythm.intra.join(",")} = ${r.rhythm.ratio}:1` +
          `  hover ${b.rowBgRest} → ${h.rowBgHover}` +
          `  ramp@110ms ${h.ramp110 ?? "-"}` +
          `  overlap ${h.overlapArea}`,
      );
    }
  }
}
const control = pick("gprimeslow", "1440", "light");
if (control) {
  const h = control.hover["cont-long"];
  console.log(
    `\ncontrol (wash on the shipped 1s row transition): ramp@110ms ${h.ramp110} · @260ms ${h.ramp260} → settled ${h.rowBgHover}`,
  );
}

if (errors.length) {
  console.error("\nFAIL\n" + [...new Set(errors)].join("\n"));
  process.exit(1);
}
console.log(`\nOK — ${frames.length} frames + measurements.json → ${outDir}`);
