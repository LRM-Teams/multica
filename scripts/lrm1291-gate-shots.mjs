/**
 * LRM-1291 gate shots — real `ResearchStageTimeline` in real Chromium, live-DOM
 * assertions first, PNGs only if every assertion holds.
 *
 * Every AC in this slice is invisible to jsdom:
 *   - dark band hues are authored `oklch()`; the upcoming hatch and the current
 *     row wash are `color-mix()` → only Chromium resolves them to sRGB
 *   - "animation on the current segment only" is a computed `animation-name`
 *   - "no overflow at ≤767" is layout (scrollWidth vs clientWidth)
 *   - "band is continuous" is geometry (adjacent segment rects must abut)
 *   - the reduced-motion downgrade is a media query
 *
 * Usage:
 *   node scripts/lrm1291-gate-shots.mjs before   # pre-fix files checked out
 *   node scripts/lrm1291-gate-shots.mjs after    # fix applied
 *
 * `before` asserts the grey stepper reproduces (no band, no stage hues).
 * `after`  asserts the full frozen spec: 9px continuous band, three visually
 *          distinct states with shape+text redundancy, exactly one animated
 *          node, 2.4s linear sheen, reduced-motion → `animation-name: none`
 *          with the band/ring/state text still present, text ≥4.5:1 and band
 *          ≥3:1 in BOTH themes, and no horizontal overflow at 360/700/767.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1291/1271.
 */
import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const label = process.argv[2];
if (label !== "before" && label !== "after") {
  throw new Error("usage: node scripts/lrm1291-gate-shots.mjs <before|after>");
}

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1291");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5291/";
const revision = execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
const MIN_TEXT = 4.5;
/** WCAG 1.4.11 — meaningful graphics (the 9px band) need 3:1, not 4.5:1. */
const MIN_GRAPHIC = 3;
/** Frozen spec: 2.4s linear sheen. */
const SHEEN_SECONDS = 2.4;

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});

const errors = [];
const ignorable = (t) => t.includes("favicon.ico") || t.includes("/api/");

const fail = (msg) => {
  throw new Error(msg);
};

/** Runs inside the page: paints colors to a canvas to get true sRGB + alpha. */
function collect() {
  const canvas = document.createElement("canvas");
  canvas.width = 1;
  canvas.height = 1;
  const ctx = canvas.getContext("2d", { willReadFrequently: true });

  const parse = (value) => {
    if (!value || value === "transparent" || value === "none") return null;
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
  /** Opaque backdrop behind `el`, walking up through transparent ancestors. */
  const backdrop = (el) => {
    let cur = el;
    while (cur && cur !== document.documentElement) {
      const c = parse(getComputedStyle(cur).backgroundColor);
      if (c && c.a >= 0.999) return c;
      cur = cur.parentElement;
    }
    const html = parse(getComputedStyle(document.documentElement).backgroundColor);
    return html && html.a >= 0.999 ? html : { r: 255, g: 255, b: 255, a: 1 };
  };
  /** Product of `opacity` on the element and every ancestor. */
  const opacityChain = (el) => {
    let acc = 1;
    const contributors = [];
    let cur = el;
    while (cur && cur !== document.documentElement) {
      const o = Number.parseFloat(getComputedStyle(cur).opacity);
      if (Number.isFinite(o) && o < 1) {
        acc *= o;
        contributors.push({
          tag: cur.tagName.toLowerCase(),
          cls: String(cur.className || "").slice(0, 70),
          opacity: o,
        });
      }
      cur = cur.parentElement;
    }
    return { product: acc, contributors };
  };
  const textContrast = (el) => {
    const cs = getComputedStyle(el);
    const fg = parse(cs.color);
    if (!fg) return null;
    const chain = opacityChain(el);
    const effective = { ...fg, a: fg.a * chain.product };
    const bg = backdrop(el);
    return {
      text: (el.textContent || "").trim().slice(0, 24),
      color: cs.color,
      fontSize: cs.fontSize,
      effectiveAlpha: Number(effective.a.toFixed(3)),
      opacityContributors: chain.contributors,
      ratio: Number(ratio(over(effective, bg), bg).toFixed(2)),
    };
  };

  const out = { cases: [], viewport: { w: innerWidth, h: innerHeight } };

  for (const caseEl of document.querySelectorAll("[data-case]")) {
    const nav = caseEl.querySelector('[data-testid="research-stage-timeline"]');
    if (!nav) continue;
    const ol = nav.querySelector("ol");
    const items = [...nav.querySelectorAll("li")];
    const bands = [...nav.querySelectorAll("[data-stage-band]")];

    const bandRects = bands.map((b) => {
      const r = b.getBoundingClientRect();
      const cs = getComputedStyle(b);
      const bgColor = parse(cs.backgroundColor);
      const bd = backdrop(b.parentElement ?? b);
      return {
        state: b.dataset.stageBand,
        left: Number(r.left.toFixed(2)),
        right: Number(r.right.toFixed(2)),
        height: Number(r.height.toFixed(2)),
        width: Number(r.width.toFixed(2)),
        backgroundColor: cs.backgroundColor,
        backgroundImage: cs.backgroundImage.slice(0, 160),
        hasGradient: cs.backgroundImage !== "none",
        /**
         * Distinct color stops actually present in the gradient. Chromium
         * resolves `var()`/`oklch()`/`color-mix()` before serializing
         * `backgroundImage`, so identical stops surface as duplicates here —
         * which is how a flat "gradient" gets caught.
         */
        gradientStops: [
          ...new Set(
            (cs.backgroundImage.match(/(?:rgba?|color|oklch|lab|lch)\([^()]*\)/g) ?? [])
              .map((s) => s.trim())
              .filter((s) => !/,\s*0\)\s*$/.test(s)),
          ),
        ],
        borderRadius: cs.borderRadius,
        // For a solid `done` band the fill itself is the graphic; for gradient
        // bands the stops are read from `backgroundImage` (recorded above) and
        // the numeric floor is checked on the solid stage hue.
        contrastVsBackdrop:
          bgColor && bgColor.a > 0.01
            ? Number(ratio(over(bgColor, bd), bd).toFixed(2))
            : null,
      };
    });

    const animated = [...nav.querySelectorAll("*")]
      .map((el) => ({ el, cs: getComputedStyle(el) }))
      .filter(({ cs }) => cs.animationName && cs.animationName !== "none")
      .map(({ el, cs }) => ({
        selector: el.dataset.stageSheen !== undefined ? "[data-stage-sheen]" : el.tagName,
        animationName: cs.animationName,
        duration: cs.animationDuration,
        timing: cs.animationTimingFunction,
        iteration: cs.animationIterationCount,
        inCurrentSegment: Boolean(el.closest('[data-stage-state="current"]')),
      }));

    const glyphs = items.map((li) => {
      const ring = li.querySelector("[data-stage-current-ring]");
      const check = li.querySelector("svg");
      const r = ring?.getBoundingClientRect();
      return {
        stage: li.dataset.stage,
        state: li.dataset.stageState,
        hasRing: Boolean(ring),
        ringSize: r ? [Number(r.width.toFixed(1)), Number(r.height.toFixed(1))] : null,
        hasCheck: Boolean(check),
        stateText: li.querySelector("[data-stage-state-text]")?.textContent ?? null,
        visibleStateText: [...li.querySelectorAll("[data-stage-state-text]")]
          .filter((el) => el.offsetParent !== null)
          .map((el) => (el.textContent || "").trim())
          .join(" ") || null,
        accessibleName: li.querySelector("button")?.getAttribute("aria-label") ?? null,
        ariaCurrent: li.querySelector("button")?.getAttribute("aria-current") ?? null,
        disabled: li.querySelector("button")?.disabled ?? null,
        visibleLabels: [...li.querySelectorAll("span")]
          .filter((s) => s.offsetParent !== null && (s.textContent || "").trim())
          .map((s) => (s.textContent || "").trim()),
      };
    });

    const texts = [...nav.querySelectorAll("*")]
      .filter((el) => {
        const own = [...el.childNodes]
          .filter((n) => n.nodeType === 3)
          .map((n) => n.textContent ?? "")
          .join("")
          .trim();
        return own.length > 0 && el.offsetParent !== null;
      })
      .map(textContrast)
      .filter(Boolean);

    out.cases.push({
      id: caseEl.dataset.case,
      trackScrollWidth: ol ? ol.scrollWidth : null,
      trackClientWidth: ol ? ol.clientWidth : null,
      navRole: nav.tagName.toLowerCase(),
      buttonCount: nav.querySelectorAll("button").length,
      ariaCurrentCount: nav.querySelectorAll('[aria-current="step"]').length,
      bandRects,
      animated,
      glyphs,
      texts,
    });
  }
  return out;
}

async function probe(page, theme) {
  await page.goto(`${base}?theme=${theme}`, { waitUntil: "networkidle" });
  await page.waitForSelector('[data-testid="research-stage-timeline"]');
  await page.waitForTimeout(250);
  return page.evaluate(collect);
}

function assertAfter(data, theme, width, reducedMotion) {
  const tag = `${theme}/${width}${reducedMotion ? "/reduced-motion" : ""}`;
  if (data.cases.length !== 3) fail(`${tag}: expected 3 cases, got ${data.cases.length}`);

  for (const c of data.cases) {
    const at = `${tag} ${c.id}`;

    // --- structure & a11y boundary preserved -------------------------------
    if (c.navRole !== "nav") fail(`${at}: host is <${c.navRole}>, expected <nav>`);
    if (c.buttonCount !== 4) fail(`${at}: ${c.buttonCount} buttons, expected 4`);
    const expectCurrent = c.id === "all-done" ? 0 : 1;
    if (c.ariaCurrentCount !== expectCurrent) {
      fail(`${at}: aria-current=step ×${c.ariaCurrentCount}, expected ${expectCurrent}`);
    }

    // --- 9px continuous band ----------------------------------------------
    if (c.bandRects.length !== 4) fail(`${at}: ${c.bandRects.length} band segments`);
    for (const b of c.bandRects) {
      if (Math.abs(b.height - 9) > 0.6) fail(`${at}: band height ${b.height}px, want 9`);
      if (b.width <= 0) fail(`${at}: band segment has zero width`);
    }
    for (let i = 1; i < c.bandRects.length; i += 1) {
      const gap = c.bandRects[i].left - c.bandRects[i - 1].right;
      if (Math.abs(gap) > 1) fail(`${at}: band gap ${gap.toFixed(2)}px between ${i - 1}/${i}`);
    }

    // --- no horizontal overflow ------------------------------------------
    if (c.trackScrollWidth - c.trackClientWidth > 1) {
      fail(`${at}: track overflows (${c.trackScrollWidth} > ${c.trackClientWidth})`);
    }

    // --- state carried by shape + text, not hue alone ---------------------
    for (const g of c.glyphs) {
      if (!g.accessibleName || !g.accessibleName.includes("·")) {
        fail(`${at} ${g.stage}: accessible name lost the full stage name (${g.accessibleName})`);
      }
      if (g.state === "done" && !g.hasCheck) fail(`${at} ${g.stage}: done without check`);
      if (g.state === "current") {
        if (!g.hasRing) fail(`${at} ${g.stage}: current without ring glyph`);
        if (Math.abs(g.ringSize[0] - 28) > 1.5) {
          fail(`${at} ${g.stage}: ring ${g.ringSize[0]}px, want 28`);
        }
      }
      // State wording is visually rendered at every width, not hidden in
      // aria-label/tooltip on narrow layouts.
      if (!g.visibleStateText) {
        fail(`${at} ${g.stage}: no visible state text at ${width}`);
      }
      // Narrow shows S1–S4; the full name must still be the accessible name.
      if (width < 768) {
        const showsShort = g.visibleLabels.some((v) => /^S[1-4]$/.test(v));
        if (!showsShort) fail(`${at} ${g.stage}: no S1–S4 short label at ${width}`);
      }
    }

    // --- exactly one moving part, and only on the current segment ---------
    if (reducedMotion) {
      if (c.animated.length !== 0) {
        fail(`${at}: reduced-motion still animates ${JSON.stringify(c.animated)}`);
      }
      // Downgrade must keep the readable state, not blank it.
      if (c.bandRects.length !== 4) fail(`${at}: reduced-motion lost the band`);
      if (c.id !== "all-done" && !c.glyphs.some((g) => g.hasRing)) {
        fail(`${at}: reduced-motion lost the current ring`);
      }
    } else {
      const want = c.id === "all-done" ? 0 : 1;
      if (c.animated.length !== want) {
        fail(`${at}: ${c.animated.length} animated nodes, want ${want}`);
      }
      for (const a of c.animated) {
        if (a.animationName !== "research-stage-sheen") {
          fail(`${at}: unexpected animation ${a.animationName}`);
        }
        const secs = Number.parseFloat(a.duration);
        if (Math.abs(secs - SHEEN_SECONDS) > 0.05) {
          fail(`${at}: sheen ${a.duration}, want ${SHEEN_SECONDS}s`);
        }
        if (a.timing !== "linear") fail(`${at}: sheen timing ${a.timing}, want linear`);
        if (!a.inCurrentSegment) fail(`${at}: animation outside the current segment`);
      }
    }

    // --- text contrast, no alpha stack on labels --------------------------
    for (const t of c.texts) {
      if (t.opacityContributors.length > 0) {
        fail(`${at} "${t.text}": opacity ancestor ${JSON.stringify(t.opacityContributors)}`);
      }
      if (t.ratio < MIN_TEXT) {
        fail(`${at} "${t.text}": ${t.ratio}:1 < ${MIN_TEXT} (${t.color} @ ${t.fontSize})`);
      }
    }

    // --- band is a meaningful graphic → 3:1 floor on the solid fills ------
    for (const b of c.bandRects) {
      if (b.state === "done" && b.contrastVsBackdrop !== null) {
        if (b.contrastVsBackdrop < MIN_GRAPHIC) {
          fail(`${at}: done band ${b.contrastVsBackdrop}:1 < ${MIN_GRAPHIC}`);
        }
      }
      if (b.state === "current") {
        if (!b.hasGradient) fail(`${at}: current band is not a gradient`);
        // A gradient whose stops are the same color paints flat and is
        // indistinguishable from `done`. Require ≥2 distinct rgb stops.
        if (b.gradientStops.length < 2) {
          fail(`${at}: current band has ${b.gradientStops.length} distinct stops, want ≥2 (${b.backgroundImage})`);
        }
      }
      if (b.state === "upcoming" && !b.backgroundImage.includes("repeating-linear-gradient")) {
        fail(`${at}: upcoming band is not hatched (${b.backgroundImage})`);
      }
    }
  }
}

function assertBefore(data, theme) {
  for (const c of data.cases) {
    if (c.bandRects.length !== 0) {
      fail(`${theme} ${c.id}: BEFORE already has a band — wrong tree checked out`);
    }
  }
}

const VIEWPORTS = [1440, 768, 767, 700, 360];
const measurements = { label, revision, generatedAt: new Date().toISOString(), runs: [] };

for (const theme of ["light", "dark"]) {
  for (const width of VIEWPORTS) {
    const page = await browser.newPage({ viewport: { width, height: 900 } });
    page.on("pageerror", (e) => errors.push(String(e)));
    page.on("console", (m) => {
      if (m.type() === "error" && !ignorable(m.location().url + m.text())) {
        errors.push(m.text());
      }
    });

    const data = await probe(page, theme);
    if (label === "after") assertAfter(data, theme, width, false);
    else assertBefore(data, theme);
    measurements.runs.push({ theme, width, reducedMotion: false, ...data });

    await page.screenshot({
      path: resolve(outDir, `${label}-${theme}-${width}.png`),
      fullPage: true,
    });

    // Reduced motion is a separate run: the media query has to be emulated.
    if (label === "after") {
      await page.emulateMedia({ reducedMotion: "reduce" });
      const rm = await probe(page, theme);
      assertAfter(rm, theme, width, true);
      measurements.runs.push({ theme, width, reducedMotion: true, ...rm });
      if (width === 1440) {
        await page.screenshot({
          path: resolve(outDir, `${label}-${theme}-${width}-reduced-motion.png`),
          fullPage: true,
        });
      }
    }
    await page.close();
  }
}

writeFileSync(
  resolve(outDir, `${label}-measurements.json`),
  `${JSON.stringify(measurements, null, 2)}\n`,
);

if (errors.length) fail(`console/page errors: ${JSON.stringify(errors.slice(0, 5))}`);

await browser.close();

const mid = measurements.runs.find((r) => r.width === 1440 && !r.reducedMotion);
const midCase = mid?.cases.find((c) => c.id === "mid-running");
console.log(`[lrm1291:${label}] all assertions passed`);
console.log(
  `[lrm1291:${label}] 1440/light mid-running band states =`,
  midCase?.bandRects.map((b) => `${b.state}@${b.height}px`).join(" "),
);
console.log(
  `[lrm1291:${label}] min text contrast per run =`,
  measurements.runs
    .filter((r) => !r.reducedMotion)
    .map((r) => {
      const min = Math.min(...r.cases.flatMap((c) => c.texts.map((t) => t.ratio)));
      return `${r.theme}/${r.width}:${min}`;
    })
    .join(" "),
);
console.log(`[lrm1291:${label}] artifacts → ${outDir}`);
