/**
 * LRM-1228 gate shots — real Chromium at a real 360px viewport.
 *
 * AC asks for a measurable occlusion / hit-target number, which jsdom cannot
 * produce (no layout). The probe:
 *   1. asserts every harness class fragment IS the real one (AFTER === working
 *      tree component, BEFORE === `git show origin/dev:<component>`), so the
 *      shots and numbers cannot describe a fiction;
 *   2. measures, per gate, the remove button's visual box, its real pointer box
 *      (`::after` included), the filename's usable width, and how much of the
 *      filename the button covers;
 *   3. writes one PNG per gate and fails if the frozen contract is not met
 *      (20px visual, ≥24px pointer target, zero text occlusion).
 *
 * Temporary tooling: delete after the shots are attached to LRM-1228.
 */
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1228");
mkdirSync(outDir, { recursive: true });

const COMPONENT = "packages/views/channels/components/composer-attachment-tray.tsx";
const HARNESS = resolve(root, "scripts/lrm1228-harness/main.tsx");

const afterSrc = readFileSync(resolve(root, COMPONENT), "utf8");
const beforeSrc = execFileSync("git", ["show", `origin/dev:${COMPONENT}`], {
  cwd: root,
  encoding: "utf8",
});
const harnessSrc = readFileSync(HARNESS, "utf8");

const harnessConst = (name) => {
  const match = new RegExp(
    `export const ${name} =\\s*(?:\\n\\s*)?["'](.+?)["'];`,
    "s",
  ).exec(harnessSrc);
  if (!match) throw new Error(`harness is missing ${name}`);
  return match[1];
};

// 1 — source-truth assertions. Each harness fragment must appear verbatim in the
// side of history it claims to represent.
const drift = [];
const mustContain = (label, source, name) => {
  const value = harnessConst(name);
  if (!source.includes(value)) drift.push(`${label} no longer contains ${name}: ${value}`);
  return value;
};
for (const name of ["CHIP_BASE_CLASS", "TRAY_CLASS"]) {
  // Composed by cn() in the component, so check the literal pieces.
  for (const piece of harnessConst(name).split(" ")) {
    if (!afterSrc.includes(piece)) drift.push(`AFTER lost tray/chip piece \`${piece}\``);
  }
}
const beforeRemove = mustContain("origin/dev", beforeSrc, "BEFORE_X_CLASS");
mustContain("origin/dev", beforeSrc, "BEFORE_CHIP_PAD_CLASS");
mustContain("origin/dev", beforeSrc, "BEFORE_REMOVE_HOLDER_CLASS");
mustContain("working tree", afterSrc, "AFTER_CHIP_PAD_CLASS");
mustContain("working tree", afterSrc, "AFTER_REMOVE_HOLDER_CLASS");

// BEFORE's button size came from `iconBtn` (isMobile ? size-9 : size-6), so
// assert that indirection instead of a composed string.
if (!/const iconBtn = isMobile \? "size-9" : "size-6";/.test(beforeSrc))
  drift.push("origin/dev no longer sizes the chip remove button via iconBtn size-9/size-6");
if (!beforeSrc.includes('cn(iconBtn, "bg-transparent hover:bg-background/80")'))
  drift.push("origin/dev no longer renders the in-chip remove button from iconBtn");
if (beforeRemove !== "size-3.5") drift.push("BEFORE_X_CLASS must be the iconGlyph mobile value");

// AFTER must apply the corner rule unconditionally (no `showImage ?` fork left).
const afterRemove = harnessConst("AFTER_REMOVE_CLASS").replace(/ opacity-100$/, "");
if (!afterSrc.includes('"relative size-5 rounded-full border border-border bg-background/95 shadow-sm"'))
  drift.push("working tree lost the 20px corner remove class");
if (!afterSrc.includes('after:absolute after:-inset-0.5 after:content-[""]'))
  drift.push("working tree lost the 24px ::after hit target");
if (afterSrc.includes('cn(iconBtn, "bg-transparent hover:bg-background/80")'))
  drift.push("working tree still has the old in-chip remove button");
if (drift.length) throw new Error(`harness drifted from source:\n- ${drift.join("\n- ")}`);
void afterRemove;

const url = process.env.HARNESS_URL ?? "http://localhost:5204/";
const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});
// 360px = the narrow phone width the AC names.
const page = await browser.newPage({
  viewport: { width: 360, height: 720 },
  deviceScaleFactor: 2,
  isMobile: true,
  hasTouch: true,
});
const errors = [];
page.on("pageerror", (e) => errors.push(String(e)));
page.on("console", (m) => {
  if (m.type() === "error" && !m.location().url.includes("favicon")) errors.push(m.text());
});

const report = [];
for (const gate of ["before", "after"]) {
  await page.goto(`${url}?gate=${gate}`, { waitUntil: "networkidle" });
  await page.locator('[data-testid="composer-attachment-tray"]').waitFor();

  const measured = await page.evaluate(() => {
    const round = (n) => Math.round(n * 100) / 100;
    const boxOf = (el) => {
      const r = el.getBoundingClientRect();
      return { x: round(r.x), y: round(r.y), w: round(r.width), h: round(r.height) };
    };
    const overlap = (a, b) => {
      const w = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x);
      const h = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y);
      return w > 0 && h > 0 ? round(w * h) : 0;
    };

    return ["file", "stale"].map((id) => {
      const chip = document.querySelector(`[data-testid="composer-tray-item-${id}"]`);
      const name = document.querySelector(`[data-testid="composer-tray-name-${id}"]`);
      const remove = document.querySelector(`[data-testid="composer-tray-remove-${id}"]`);
      const chipBox = boxOf(chip);
      const nameBox = boxOf(name);
      const removeBox = boxOf(remove);

      // The pointer target is the visual box grown by the ::after inset (or the
      // visual box itself when there is no pad).
      const pseudo = getComputedStyle(remove, "::after");
      const pad =
        pseudo.content && pseudo.content !== "none"
          ? Math.abs(Number.parseFloat(pseudo.insetBlockStart || pseudo.top || "0")) || 0
          : 0;
      const hit = {
        x: removeBox.x - pad,
        y: removeBox.y - pad,
        w: removeBox.w + pad * 2,
        h: removeBox.h + pad * 2,
      };

      return {
        chipId: id,
        chipWidth: chipBox.w,
        chipHeight: chipBox.h,
        filenameWidth: nameBox.w,
        removeVisual: `${removeBox.w}×${removeBox.h}`,
        removeVisualArea: round(removeBox.w * removeBox.h),
        removeHitTarget: `${round(hit.w)}×${round(hit.h)}`,
        // How much of the chip's own width the button eats out of the layout.
        removeInFlow: removeBox.x + removeBox.w <= chipBox.x + chipBox.w,
        filenameOccludedPx2: overlap(removeBox, nameBox),
        // Anything painted above the tray's own top edge is what `pt-2` reserves.
        outdentTopPx: round(Math.max(0, chipBox.y - removeBox.y)),
        outdentRightPx: round(Math.max(0, removeBox.x + removeBox.w - (chipBox.x + chipBox.w))),
      };
    });
  });

  report.push({ gate, chips: measured });
  await page.screenshot({
    path: resolve(outDir, `file-chip-remove-360--${gate}.png`),
    fullPage: false,
  });
  await page
    .locator('[data-testid="composer-shell"]')
    .screenshot({ path: resolve(outDir, `file-chip-remove-360--${gate}--crop.png`) });

  // The clipping trap LRM-1180 hit: `overflow-y-hidden` clips at the *padding*
  // box and `overflow-x-auto` clips the scroll end, so the last chip's outdented
  // button only survives because the tray reserves `pt-2 pr-2`. Verify it at the
  // scroll end, where the right outdent has the least room.
  const clip = await page.evaluate(() => {
    const tray = document.querySelector('[data-testid="composer-attachment-tray"]');
    tray.scrollLeft = tray.scrollWidth;
    const remove = document.querySelector('[data-testid="composer-tray-remove-stale"]');
    const t = tray.getBoundingClientRect();
    const r = remove.getBoundingClientRect();
    const style = getComputedStyle(tray);
    const pad = (v) => Number.parseFloat(v) || 0;
    return {
      // Padding box = the clip region for overflow-y-hidden.
      topRoom: Math.round((r.top - (t.top + 0)) * 100) / 100,
      rightRoom: Math.round((t.right - r.right) * 100) / 100,
      reservedTop: pad(style.paddingTop),
      reservedRight: pad(style.paddingRight),
    };
  });
  report[report.length - 1].scrollEndClip = clip;
  await page
    .locator('[data-testid="composer-shell"]')
    .screenshot({
      path: resolve(outDir, `file-chip-remove-360--${gate}--scroll-end.png`),
    });
}

const before = report.find((r) => r.gate === "before");
const after = report.find((r) => r.gate === "after");
const fail = [];
for (const [i, chip] of after.chips.entries()) {
  const was = before.chips[i];
  if (chip.removeVisual !== "20×20")
    fail.push(`${chip.chipId}: AFTER remove is ${chip.removeVisual}, frozen spec is 20×20`);
  const [hitW, hitH] = chip.removeHitTarget.split("×").map(Number);
  if (hitW < 24 || hitH < 24)
    fail.push(`${chip.chipId}: AFTER pointer target ${chip.removeHitTarget} < 24×24 (SC 2.5.8)`);
  if (chip.filenameOccludedPx2 !== 0)
    fail.push(`${chip.chipId}: AFTER button covers ${chip.filenameOccludedPx2}px² of the filename`);
  if (chip.removeInFlow)
    fail.push(`${chip.chipId}: AFTER remove is still inside the chip box (no outdent)`);
  if (was.removeVisual !== "36×36")
    fail.push(`${chip.chipId}: BEFORE did not reproduce the 36px button (got ${was.removeVisual})`);
  if (chip.filenameWidth <= was.filenameWidth)
    fail.push(
      `${chip.chipId}: AFTER filename width ${chip.filenameWidth} did not grow past BEFORE ${was.filenameWidth}`,
    );
}
if (errors.length) fail.push(`console/page errors: ${errors.join(" | ")}`);
// The last chip's outdented button must be fully inside the tray's clip region.
if (after.scrollEndClip.topRoom < 0)
  fail.push(
    `AFTER last chip's button is clipped above the tray by ${-after.scrollEndClip.topRoom}px (pt-2 = ${after.scrollEndClip.reservedTop}px)`,
  );
if (after.scrollEndClip.rightRoom < 0)
  fail.push(
    `AFTER last chip's button overflows the tray's right edge by ${-after.scrollEndClip.rightRoom}px (pr-2 = ${after.scrollEndClip.reservedRight}px)`,
  );
if (fail.length) throw new Error(`gate failed:\n- ${fail.join("\n- ")}`);

console.log(
  JSON.stringify(
    report.map((r) => ({
      gate: r.gate,
      chips: r.chips,
    })),
    null,
    2,
  ),
);
for (const [i, chip] of after.chips.entries()) {
  const was = before.chips[i];
  const areaDrop = Math.round((1 - chip.removeVisualArea / was.removeVisualArea) * 1000) / 10;
  const nameGain = Math.round((chip.filenameWidth - was.filenameWidth) * 10) / 10;
  console.log(
    `${chip.chipId}: button ${was.removeVisual} → ${chip.removeVisual} (area -${areaDrop}%), ` +
      `filename ${was.filenameWidth}px → ${chip.filenameWidth}px (+${nameGain}px), ` +
      `hit target ${was.removeHitTarget} → ${chip.removeHitTarget}, text occlusion ${chip.filenameOccludedPx2}px²`,
  );
}
console.log("shots ->", outDir);
await browser.close();
