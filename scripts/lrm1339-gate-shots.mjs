/**
 * LRM-1339 gate shots — real `ResearchProductRoundCardView` in a real browser,
 * live-DOM contrast assertions, then PNGs.
 *
 * The defect is an alpha stack on a semantic tone, not a wrong token: the
 * summary spans inherit `text-brand` / `text-success` / `text-warning` /
 * `text-muted-foreground` from `decisionTone`, sit on the matching low-alpha
 * wash, and then multiply that tone by `opacity-80` (11px note) or `opacity-70`
 * (10px countdown / budget). The `goal_patch` old-goal line stacked
 * `text-muted-foreground` with `opacity-70`. jsdom neither resolves the tokens
 * nor composites alpha, so a unit test can only guard class names — the real
 * WCAG number has to come from Chromium.
 *
 * Usage:
 *   node scripts/lrm1339-gate-shots.mjs before   # run with the pre-fix file
 *   node scripts/lrm1339-gate-shots.mjs after    # run with the fix applied
 *
 * `before` asserts the defect reproduces (< 4.5:1 in light on at least one site).
 * `after`  asserts every measured site clears 4.5:1 in light AND dark, carries no
 *          residual alpha, and that the four decision tones stay distinguishable.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1339.
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const label = process.argv[2];
if (label !== "before" && label !== "after" && label !== "report") {
  throw new Error("usage: node scripts/lrm1339-gate-shots.mjs <before|after|report>");
}

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1339");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5339/";
const MIN_CONTRAST = 4.5;
const TONES = ["continue", "stop_enough", "stop_budget", "default"];

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});
const page = await browser.newPage({ viewport: { width: 620, height: 620 } });

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

/**
 * The measurement kernel is identical to LRM-1252's: paint the computed color
 * into a 1×1 canvas to escape `oklch()`/`color-mix()` serialization, multiply
 * the ancestor `opacity` chain into the alpha, composite onto the real backdrop
 * stack, then run WCAG 2.x relative luminance.
 */
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
    const la = lum(a); const lb = lum(b);
    return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
  };

  const opacityChain = (el) => {
    let acc = 1; const seen = []; let cur = el;
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

  const backdrop = (el) => {
    const stack = []; let cur = el;
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
      backdrop: \`rgb(\${bg.r.toFixed(0)}, \${bg.g.toFixed(0)}, \${bg.b.toFixed(0)})\`,
      contrast: Number(ratio(effective, bg).toFixed(2)),
    };
  };

  return measure;
})()`;

async function probeSummary(theme) {
  await page.goto(`${base}?theme=${theme}&case=summary`, { waitUntil: "networkidle" });
  // The pre-fix source carries no data-testid, so the BEFORE run falls back to
  // structural selectors (`[data-case] > button` and its span order).
  await page.waitForSelector('[data-case="continue"] button');
  await page.waitForTimeout(250);

  return page.evaluate(
    ({ kernel, tones }) => {
      // eslint-disable-next-line no-eval
      const m = eval(kernel);
      const sites = [];
      const tonesOut = {};

      for (const tone of tones) {
        const row = document.querySelector(`[data-case="${tone}"]`);
        if (!row) return { error: `tone case missing: ${tone}` };
        const button =
          row.querySelector('[data-testid="research-round-summary"]') ??
          row.querySelector("button");
        if (!button) return { error: `summary button missing: ${tone}` };

        const spans = [...button.children].filter((el) => el.tagName === "SPAN");
        const pick = (id, index) =>
          button.querySelector(`[data-testid="${id}"]`) ?? spans[index] ?? null;
        // Span order in both revisions: label · note · countdown · budget.
        const strong = spans[0];
        const note = pick("research-round-summary-note", 1);
        const countdown = pick("research-round-summary-countdown", 2);
        const budget = pick("research-round-summary-budget", 3);
        if (!note || !countdown || !budget || !strong) {
          return { error: `summary spans missing: ${tone} (${spans.length} spans)` };
        }

        sites.push(m(note, `${tone}.note_11px`));
        sites.push(m(countdown, `${tone}.countdown_10px`));
        sites.push(m(budget, `${tone}.budget_10px`));
        tonesOut[tone] = {
          buttonClass: String(button.className || ""),
          strong: m(strong, `${tone}.label`),
          note: m(note, `${tone}.note`),
          countdown: m(countdown, `${tone}.countdown`),
          borderColor: getComputedStyle(button).borderTopColor,
          backgroundColor: getComputedStyle(button).backgroundColor,
        };
      }

      return { sites, tones: tonesOut };
    },
    { kernel: KERNEL, tones: TONES },
  );
}

async function probeDetail(theme) {
  await page.goto(`${base}?theme=${theme}&case=detail`, { waitUntil: "networkidle" });
  await page.waitForSelector("[role=dialog]");
  await page.waitForTimeout(250);

  return page.evaluate(
    ({ kernel }) => {
      // eslint-disable-next-line no-eval
      const m = eval(kernel);
      // BEFORE has no testids: the old goal is the only line-through <p>, and the
      // capped note is the only span inside the decision chip.
      const oldGoal =
        document.querySelector('[data-testid="research-round-goal-current"]') ??
        [...document.querySelectorAll("[role=dialog] p")].find((p) =>
          /line-through/.test(getComputedStyle(p).textDecorationLine),
        );
      const capped =
        document.querySelector('[data-testid="research-round-budget-capped"]') ??
        [...document.querySelectorAll("[role=dialog] span")].find((s) =>
          /font-normal/.test(String(s.className || "")),
        );
      if (!oldGoal || !capped) return { error: "detail sites missing" };
      return {
        sites: [m(oldGoal, "detail.goal_current_11px"), m(capped, "detail.budget_capped")],
        lineThrough: getComputedStyle(oldGoal).textDecorationLine,
      };
    },
    { kernel: KERNEL },
  );
}

const report = { label, themes: {} };

for (const theme of ["light", "dark"]) {
  const summary = await probeSummary(theme);
  if (summary.error) fail(`[${theme}/summary] ${summary.error}`);

  writeFileSync(
    resolve(outDir, `${label}-${theme}-summary.png`),
    await page.screenshot({ fullPage: true }),
  );

  const detail = await probeDetail(theme);
  if (detail.error) fail(`[${theme}/detail] ${detail.error}`);

  writeFileSync(
    resolve(outDir, `${label}-${theme}-detail.png`),
    await page.screenshot({ fullPage: false }),
  );

  const sites = [...summary.sites, ...detail.sites];
  report.themes[theme] = { sites, tones: summary.tones, lineThrough: detail.lineThrough };

  if (label === "after") {
    const violations = [];
    for (const site of sites) {
      if (site.contrast < MIN_CONTRAST) {
        violations.push(
          `${site.name}: contrast ${site.contrast} < ${MIN_CONTRAST} (${site.computedColor} @ alpha ${site.effectiveAlpha} on ${site.backdrop})`,
        );
      }

      // The slice's own invariant applies to EVERY site, exemption or not:
      // no residual alpha anywhere on these text nodes.
      if (site.effectiveAlpha < 0.999) {
        violations.push(
          `${site.name}: still dimmed, effective alpha ${site.effectiveAlpha} via ${JSON.stringify(site.opacityContributors)}`,
        );
      }
      if (/\bopacity-\d/.test(site.className)) {
        violations.push(`${site.name}: still carries opacity-* (${site.className})`);
      }
    }
    if (violations.length > 0) {
      fail(`[after/${theme}]\n  - ${violations.join("\n  - ")}`);
    }

    // Hierarchy must survive on font metrics alone, not alpha.
    for (const tone of TONES) {
      const t = report.themes[theme].tones[tone];
      if (t.strong.fontWeight === t.note.fontWeight) {
        fail(
          `[after/${theme}] ${tone}: label/note share font-weight ${t.strong.fontWeight}`,
        );
      }
      if (t.note.fontSize === t.countdown.fontSize) {
        fail(`[after/${theme}] ${tone}: note/countdown share font-size ${t.note.fontSize}`);
      }
      if (!/mono/i.test(t.countdown.fontFamily)) {
        fail(`[after/${theme}] ${tone}: countdown lost mono (${t.countdown.fontFamily})`);
      }
      if (/\bopacity-\d/.test(t.buttonClass)) {
        fail(`[after/${theme}] ${tone}: summary button carries opacity-*`);
      }
    }

    // The four decision tones must remain distinguishable by color, not alpha.
    const colors = new Set(TONES.map((t) => report.themes[theme].tones[t].note.computedColor));
    if (colors.size < 3) {
      fail(
        `[after/${theme}] decision tones collapsed to ${colors.size} distinct colors: ${[...colors].join(" | ")}`,
      );
    }
    // Deprecation signal on the old goal must still be the strike-through.
    if (!/line-through/.test(report.themes[theme].lineThrough)) {
      fail(`[after/${theme}] old-goal lost line-through (${report.themes[theme].lineThrough})`);
    }
  }

  if (label === "before" && theme === "light") {
    const failing = sites.filter((s) => s.contrast < MIN_CONTRAST);
    if (failing.length === 0) {
      fail("[before/light] every site already passes — BEFORE baseline did not reproduce");
    }
  }
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
console.log(`\n${label}: 4 PNGs + measurements.json → ${outDir}`);
