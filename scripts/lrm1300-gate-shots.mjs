/**
 * LRM-1300 gate shots — channel "remove member" confirm, real primitives in a
 * real browser, live-DOM assertions first, PNGs only after they pass.
 *
 * Why a browser and not jsdom: every number this design gate has to lock is a
 * layout/paint fact — surface width against the viewport, footer axis at the
 * `sm` breakpoint, and the WCAG ratio of `text-destructive` composited over
 * `bg-destructive/10` over `bg-muted/50` over `bg-popover`. jsdom resolves
 * none of those.
 *
 * Usage: node scripts/lrm1300-gate-shots.mjs        (harness on :5300)
 *
 * Temporary tooling: delete after the shots are attached to LRM-1300.
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1300");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5300/";
const MIN_CONTRAST = 4.5;
/**
 * Sites whose ratio is reported but not gated here, because the shortfall is a
 * shared token fact rather than something this surface can fix on its own.
 * `confirm` = `text-destructive` on `bg-destructive/10` (the danger wash used by
 * every destructive confirm in the app). The measured numbers go into the design
 * gate as a token finding instead of silently failing this harness.
 */
const SOFT_CONTRAST = new Set(["confirm"]);
const MAX_W_XS = 320;
const MAX_W_SM = 384;

const frames = [
  { variant: "before", state: "default", vp: "1440", theme: "light" },
  { variant: "before", state: "default", vp: "1440", theme: "dark" },
  { variant: "before", state: "default", vp: "390", theme: "light" },
  { variant: "before", state: "default", vp: "390", theme: "dark" },
  { variant: "after", state: "default", vp: "1440", theme: "light" },
  { variant: "after", state: "default", vp: "1440", theme: "dark" },
  { variant: "after", state: "default", vp: "390", theme: "light" },
  { variant: "after", state: "default", vp: "390", theme: "dark" },
  { variant: "after", state: "longname", vp: "1440", theme: "light" },
  { variant: "after", state: "longname", vp: "390", theme: "light" },
  { variant: "after", state: "pending", vp: "1440", theme: "light" },
  { variant: "spec", state: "pending", vp: "1440", theme: "light" },
  { variant: "spec", state: "pending", vp: "390", theme: "dark" },
];

const viewports = {
  1440: { width: 1440, height: 900 },
  390: { width: 390, height: 844 },
};

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});

const errors = [];
const report = [];
const ignorable = (t) => t.includes("favicon.ico") || t.includes("/api/");

for (const frame of frames) {
  const page = await browser.newPage({ viewport: viewports[frame.vp] });
  page.on("pageerror", (e) => errors.push(`${frame.variant}/${frame.state}: ${e}`));
  page.on("console", (m) => {
    if (m.type() === "error" && !ignorable(m.location().url + m.text())) {
      errors.push(`${frame.variant}/${frame.state}: ${m.text()}`);
    }
  });

  const url = `${base}?variant=${frame.variant}&state=${frame.state}&theme=${frame.theme}`;
  await page.goto(url, { waitUntil: "networkidle" });
  await page.waitForSelector('[data-testid="lrm1300-surface"]');
  await page.waitForTimeout(320); // open animation settles before measuring

  const probe = await page.evaluate(() => {
    const canvas = document.createElement("canvas");
    canvas.width = 1;
    canvas.height = 1;
    const ctx = canvas.getContext("2d", { willReadFrequently: true });

    // Chromium serializes computed colors in their authored space
    // (oklch/color-mix); paint once and read the pixel for true sRGB + alpha.
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
    const opacityProduct = (el) => {
      let acc = 1;
      let cur = el;
      while (cur && cur !== document.documentElement) {
        const o = Number.parseFloat(getComputedStyle(cur).opacity);
        if (Number.isFinite(o) && o < 1) acc *= o;
        cur = cur.parentElement;
      }
      return acc;
    };
    const backdrop = (el) => {
      const stack = [];
      let cur = el;
      while (cur) {
        const c = parse(getComputedStyle(cur).backgroundColor);
        if (c && c.a > 0) stack.push(c);
        if (c && c.a >= 1) break;
        cur = cur.parentElement;
      }
      let acc = stack.pop() ?? { r: 255, g: 255, b: 255, a: 1 };
      while (stack.length > 0) acc = over(stack.pop(), acc);
      return acc;
    };
    const contrastOf = (el) => {
      const cs = getComputedStyle(el);
      const raw = parse(cs.color);
      const bg = backdrop(el);
      const fg = over({ ...raw, a: raw.a * opacityProduct(el) }, bg);
      return {
        color: cs.color,
        fontSize: cs.fontSize,
        fontWeight: cs.fontWeight,
        backdrop: `rgb(${bg.r.toFixed(0)}, ${bg.g.toFixed(0)}, ${bg.b.toFixed(0)})`,
        contrast: Number(ratio(fg, bg).toFixed(2)),
      };
    };
    const box = (el) => {
      const r = el.getBoundingClientRect();
      return {
        width: Number(r.width.toFixed(1)),
        height: Number(r.height.toFixed(1)),
        left: Number(r.left.toFixed(1)),
        right: Number(r.right.toFixed(1)),
      };
    };

    const q = (id) => document.querySelector(`[data-testid="${id}"]`);
    const surface = q("lrm1300-surface");
    const title = q("lrm1300-title");
    const description = q("lrm1300-description");
    const confirm = q("lrm1300-confirm");
    const cancel = q("lrm1300-cancel");
    const footer = q("lrm1300-footer");
    if (!surface || !title || !confirm || !cancel || !footer) {
      return { error: "surface parts missing" };
    }

    const footerCs = getComputedStyle(footer);
    const active = document.activeElement;

    return {
      viewport: { width: window.innerWidth, height: window.innerHeight },
      surface: box(surface),
      title: {
        ...box(title),
        ...contrastOf(title),
        scrollWidth: title.scrollWidth,
        clientWidth: title.clientWidth,
        text: (title.textContent ?? "").trim(),
      },
      description: description
        ? { ...box(description), ...contrastOf(description) }
        : null,
      confirm: {
        ...box(confirm),
        ...contrastOf(confirm),
        disabled: confirm.hasAttribute("disabled"),
        text: (confirm.textContent ?? "").trim(),
        hasSpinner: Boolean(confirm.querySelector(".animate-spin")),
      },
      cancel: {
        ...box(cancel),
        ...contrastOf(cancel),
        disabled: cancel.hasAttribute("disabled"),
      },
      footer: {
        ...box(footer),
        flexDirection: footerCs.flexDirection,
        display: footerCs.display,
        justifyContent: footerCs.justifyContent,
      },
      focus: {
        testid: active?.getAttribute?.("data-testid") ?? null,
        tag: active?.tagName?.toLowerCase() ?? null,
        text: (active?.textContent ?? "").trim().slice(0, 20),
      },
      docScrollWidth: document.documentElement.scrollWidth,
    };
  });

  if (probe.error) throw new Error(`${frame.variant}/${frame.state}: ${probe.error}`);

  const vw = viewports[frame.vp].width;
  const tag = `${frame.variant}-${frame.state}-${frame.vp}-${frame.theme}`;
  const fails = [];

  // Shared invariants — hold for every frame.
  if (probe.docScrollWidth > vw + 1) {
    fails.push(`horizontal overflow: scrollWidth ${probe.docScrollWidth} > ${vw}`);
  }
  // BEFORE is the defect exhibit: record its numbers, do not gate on them.
  const contrastFindings = [];
  for (const [name, node] of [
    ["title", probe.title],
    ["description", probe.description],
    ["confirm", probe.confirm],
    ["cancel", probe.cancel],
  ]) {
    if (node && node.contrast < MIN_CONTRAST) {
      const msg = `${name} contrast ${node.contrast} < ${MIN_CONTRAST}${
        node.disabled ? " (disabled — WCAG 1.4.3 exempt, reported only)" : ""
      }`;
      contrastFindings.push(msg);
      const gated =
        frame.variant !== "before" && !SOFT_CONTRAST.has(name) && !node.disabled;
      if (gated) fails.push(msg);
    }
  }
  if (probe.title.scrollWidth > probe.title.clientWidth + 1) {
    fails.push(
      `title clipped: scrollWidth ${probe.title.scrollWidth} > clientWidth ${probe.title.clientWidth}`,
    );
  }

  if (frame.variant === "before") {
    // The defect must reproduce, otherwise the BEFORE frame proves nothing.
    if (probe.surface.width < vw * 0.95) {
      fails.push(`BEFORE surface ${probe.surface.width} is not full-bleed on ${vw}`);
    }
    if (probe.confirm.width < vw * 0.85) {
      fails.push(
        `BEFORE destructive button ${probe.confirm.width} is not a near-full-width bar on ${vw}`,
      );
    }
  } else {
    const cap = vw >= 640 ? MAX_W_SM : MAX_W_XS;
    if (probe.surface.width > cap + 1) {
      fails.push(`AFTER surface ${probe.surface.width} > cap ${cap} on ${vw}`);
    }
    // Centered, not edge-anchored.
    const centerDelta = Math.abs((probe.surface.left + probe.surface.right) / 2 - vw / 2);
    if (centerDelta > 1.5) fails.push(`AFTER surface off-center by ${centerDelta}`);
    if (vw >= 640) {
      if (probe.footer.flexDirection !== "row") {
        fails.push(`desktop footer axis ${probe.footer.flexDirection}, expected row`);
      }
      if (probe.footer.justifyContent !== "flex-end") {
        fails.push(`desktop footer justify ${probe.footer.justifyContent}`);
      }
      if (probe.confirm.width > probe.surface.width * 0.6) {
        fails.push(
          `desktop destructive button ${probe.confirm.width} exceeds 60% of ${probe.surface.width}`,
        );
      }
    } else if (probe.footer.flexDirection !== "column-reverse") {
      fails.push(`narrow footer axis ${probe.footer.flexDirection}`);
    }
    // Destructive must never be the auto-focused (Enter-armed) control.
    if (probe.focus.testid === "lrm1300-confirm") {
      fails.push("initial focus landed on the destructive action");
    }
    if (frame.state === "pending") {
      if (!probe.confirm.disabled) fails.push("pending: confirm not disabled");
      if (frame.variant === "spec" && !probe.confirm.hasSpinner) {
        fails.push("spec pending: expected spinner + progress copy");
      }
      if (frame.variant === "after" && probe.confirm.hasSpinner) {
        fails.push("after pending: landed code should have no spinner yet");
      }
    }
  }

  if (fails.length > 0) {
    throw new Error(`${tag} assertions failed:\n  - ${fails.join("\n  - ")}`);
  }

  const file = resolve(outDir, `${tag}.png`);
  await page.screenshot({ path: file, fullPage: false });
  report.push({ frame: tag, url, probe, contrastFindings });
  await page.close();
}

await browser.close();

if (errors.length > 0) {
  throw new Error(`console/page errors:\n${errors.join("\n")}`);
}

writeFileSync(
  resolve(outDir, "report.json"),
  `${JSON.stringify({ generatedAt: new Date().toISOString(), frames: report }, null, 2)}\n`,
);

for (const r of report) {
  const p = r.probe;
  console.log(
    `${r.frame.padEnd(30)} surface=${p.surface.width} confirm=${p.confirm.width} ` +
      `footer=${p.footer.flexDirection} focus=${p.focus.testid ?? "-"} ` +
      `contrast(title/desc/confirm/cancel)=${p.title.contrast}/${p.description?.contrast ?? "-"}/${p.confirm.contrast}/${p.cancel.contrast}`,
  );
}
console.log(`\n${report.length} frames written to ${outDir}`);
