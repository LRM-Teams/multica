/**
 * LRM-1252 gate shots — real `ResearchStageTimeline` + `ExplorationRail` in a
 * real browser, live-DOM contrast assertions, then PNGs.
 *
 * The defect is an alpha stack, not a wrong token: `text-muted-foreground/80`
 * under an ancestor `opacity-75` composites to effective alpha 0.60. jsdom
 * neither resolves the token nor composites ancestor opacity, so a unit test
 * can only guard the class names — the actual WCAG number has to come from
 * Chromium.
 *
 * Usage:
 *   node scripts/lrm1252-gate-shots.mjs before   # run with the pre-fix files
 *   node scripts/lrm1252-gate-shots.mjs after    # run with the fix applied
 *
 * `before` asserts the defect reproduces (< 4.5:1 in light).
 * `after`  asserts both sites clear 4.5:1 in light AND dark, and that the
 *          three step states stay visually distinct without alpha.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1252.
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const label = process.argv[2];
if (label !== "before" && label !== "after") {
  throw new Error("usage: node scripts/lrm1252-gate-shots.mjs <before|after>");
}

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1252");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5252/";
const MIN_CONTRAST = 4.5;

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});
const page = await browser.newPage({ viewport: { width: 900, height: 760 } });

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
  await page.waitForSelector('[data-testid="research-stage-timeline"]');
  await page.waitForSelector('[data-testid="exploration-rail-cards"]');
  await page.waitForTimeout(250);

  return page.evaluate(() => {
    const canvas = document.createElement("canvas");
    canvas.width = 1;
    canvas.height = 1;
    const ctx = canvas.getContext("2d", { willReadFrequently: true });

    /**
     * Chromium serializes computed colors in their authored color space
     * (`oklch(...)`, `color-mix(...)`). Paint once and read the pixel back to
     * get the true sRGB + alpha the user sees.
     */
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

    /** Product of `opacity` on the element and every ancestor. */
    const opacityChain = (el) => {
      let acc = 1;
      const seen = [];
      let cur = el;
      while (cur && cur !== document.documentElement) {
        const o = Number.parseFloat(getComputedStyle(cur).opacity);
        if (Number.isFinite(o) && o < 1) {
          acc *= o;
          seen.push({
            tag: cur.tagName.toLowerCase(),
            cls: String(cur.className || "").slice(0, 80),
            opacity: o,
          });
        }
        cur = cur.parentElement;
      }
      return { product: acc, contributors: seen };
    };

    /** Composite the ancestor background chain down to an opaque color. */
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

    const measure = (el, name) => {
      const cs = getComputedStyle(el);
      const raw = parse(cs.color);
      const chain = opacityChain(el);
      const bg = backdrop(el);
      const effective = over({ ...raw, a: raw.a * chain.product }, bg);
      return {
        name,
        text: (el.textContent ?? "").trim().slice(0, 40),
        className: String(el.className || ""),
        computedColor: cs.color,
        colorAlpha: Number(raw.a.toFixed(4)),
        opacityProduct: Number(chain.product.toFixed(4)),
        opacityContributors: chain.contributors,
        effectiveAlpha: Number((raw.a * chain.product).toFixed(4)),
        fontSize: cs.fontSize,
        fontWeight: cs.fontWeight,
        fontFamily: cs.fontFamily.split(",")[0],
        backdrop: `rgb(${bg.r.toFixed(0)}, ${bg.g.toFixed(0)}, ${bg.b.toFixed(0)})`,
        contrast: Number(ratio(effective, bg).toFixed(2)),
      };
    };

    const labelOf = (state) =>
      document.querySelector(
        `[data-stage-state="${state}"] span.block.truncate`,
      );

    const upcoming = labelOf("upcoming");
    const done = labelOf("done");
    const current = labelOf("current");
    const pending = [
      ...document.querySelectorAll(
        '[data-testid="exploration-result-card"] span',
      ),
    ].find((s) =>
      [...s.childNodes]
        .filter((n) => n.nodeType === 3)
        .map((n) => n.textContent ?? "")
        .join("")
        .includes("摘要生成中"),
    );

    if (!upcoming || !done || !current) return { error: "timeline states missing" };
    if (!pending) return { error: "rail pending summary missing" };

    const glyph = document.querySelector(
      '[data-stage-state="upcoming"] [aria-hidden].rounded-full',
    );
    const connector = document.querySelector(
      '[data-stage-state="upcoming"] span.h-px',
    );

    return {
      sites: [
        measure(upcoming, "timeline.upcoming_label"),
        measure(pending, "rail.pending_summary"),
      ],
      states: {
        upcoming: measure(upcoming, "upcoming"),
        current: measure(current, "current"),
        done: measure(done, "done"),
      },
      decoration: {
        upcomingRowClass: String(
          document.querySelector('[data-stage-state="upcoming"]')?.className ||
            "",
        ),
        glyphOpacity: glyph ? getComputedStyle(glyph).opacity : null,
        glyphClass: String(glyph?.className || ""),
        connectorBg: connector
          ? getComputedStyle(connector).backgroundColor
          : null,
      },
    };
  });
}

const report = { label, themes: {} };

for (const theme of ["light", "dark"]) {
  const data = await probe(theme);
  if (data.error) fail(`[${theme}] ${data.error}`);
  report.themes[theme] = data;

  for (const site of data.sites) {
    if (label === "after" && site.contrast < MIN_CONTRAST) {
      fail(
        `[after/${theme}] ${site.name} contrast ${site.contrast} < ${MIN_CONTRAST} (${site.computedColor} @ alpha ${site.effectiveAlpha} on ${site.backdrop})`,
      );
    }
    if (label === "after" && site.effectiveAlpha < 0.999) {
      fail(
        `[after/${theme}] ${site.name} still dimmed: effective alpha ${site.effectiveAlpha}`,
      );
    }
    if (label === "before" && theme === "light" && site.contrast >= MIN_CONTRAST) {
      fail(
        `[before/light] ${site.name} contrast ${site.contrast} already passes — BEFORE baseline did not reproduce`,
      );
    }
  }

  if (label === "after") {
    const { upcoming, current, done } = data.states;
    if (/opacity-\d/.test(data.decoration.upcomingRowClass)) {
      fail(`[after/${theme}] upcoming row still carries opacity-*`);
    }
    // Hierarchy must survive on font metrics alone, not alpha.
    if (current.fontWeight === upcoming.fontWeight) {
      fail(`[after/${theme}] current/upcoming share font-weight ${current.fontWeight}`);
    }
    if (done.computedColor === upcoming.computedColor) {
      fail(`[after/${theme}] done/upcoming share the exact same color`);
    }
    if (
      !/\bfont-mono\b/.test(upcoming.className) ||
      upcoming.fontSize === current.fontSize
    ) {
      fail(
        `[after/${theme}] upcoming lost its mono/size weakening (${upcoming.className} @ ${upcoming.fontSize} vs current ${current.fontSize})`,
      );
    }
    // Decorative layers must be untouched by this slice.
    if (data.decoration.glyphOpacity !== "0.7") {
      fail(
        `[after/${theme}] glyph opacity drifted to ${data.decoration.glyphOpacity} (expected 0.7)`,
      );
    }
    if (!data.decoration.connectorBg) {
      fail(`[after/${theme}] upcoming connector line disappeared`);
    }
  }

  writeFileSync(
    resolve(outDir, `${label}-${theme}-full.png`),
    await page.screenshot({ fullPage: false }),
  );
  const timeline = page.locator('[data-testid="research-stage-timeline"]');
  writeFileSync(
    resolve(outDir, `${label}-${theme}-timeline.png`),
    await timeline.screenshot(),
  );
  const rail = page.locator('[data-testid="exploration-rail"]');
  writeFileSync(
    resolve(outDir, `${label}-${theme}-rail.png`),
    await rail.screenshot(),
  );
}

if (errors.length > 0) fail(`console/page errors: ${errors.join(" | ")}`);

writeFileSync(
  resolve(outDir, `${label}-measurements.json`),
  `${JSON.stringify(report, null, 2)}\n`,
);

for (const theme of ["light", "dark"]) {
  for (const site of report.themes[theme].sites) {
    console.log(
      `${label}/${theme} ${site.name}: contrast ${site.contrast}:1 · effAlpha ${site.effectiveAlpha} · ${site.computedColor} on ${site.backdrop}`,
    );
  }
}

await browser.close();
console.log(`\n${label}: 6 PNGs + measurements.json → ${outDir}`);
