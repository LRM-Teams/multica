/**
 * LRM-1348 gate shots — real `ChannelPresenceCluster` in real Chromium.
 *
 * What only a browser can show:
 *   1. Chromium blurs the focused element the moment it becomes natively
 *      `disabled`, dropping `document.activeElement` to `<body>`.
 *   2. The Base UI Portal overlay (desktop `HoverCard`, narrow `Popover`) reads
 *      that focus-out as a dismiss and unmounts its whole subtree, so `Stop all`
 *      and the sibling rows' `Stop` leave the DOM after one keyboard Stop.
 * jsdom implements neither step, so the unit spec can only guard attributes.
 *
 * Usage:
 *   node scripts/lrm1348-gate-shots.mjs before   # component reverted to dev
 *   node scripts/lrm1348-gate-shots.mjs after    # fix applied
 *
 * `before` asserts the defect reproduces at least once (focus dropped to body
 * and/or the overlay unmounted). `after` asserts, for every theme × viewport:
 * focus stays on the very same Stop node, the overlay is still mounted, Stop all
 * is still reachable, and no button is natively disabled.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1348.
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const label = process.argv[2];
if (label !== "before" && label !== "after") {
  throw new Error("usage: node scripts/lrm1348-gate-shots.mjs <before|after>");
}

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1348");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5348/";

const CASES = [
  { viewport: { width: 1440, height: 900 }, theme: "light", surface: "hovercard" },
  { viewport: { width: 1440, height: 900 }, theme: "dark", surface: "hovercard" },
  { viewport: { width: 360, height: 780 }, theme: "light", surface: "popover" },
  { viewport: { width: 360, height: 780 }, theme: "dark", surface: "popover" },
];

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});

const results = [];
const fail = (msg) => {
  throw new Error(msg);
};

for (const testCase of CASES) {
  const { viewport, theme, surface } = testCase;
  const name = `${label}-${viewport.width}-${theme}`;
  const context = await browser.newContext({
    viewport,
    // The desktop surface is a HoverCard: it must be a fine pointer, and the
    // narrow surface must not be, or `useIsMobile` picks the wrong primitive.
    hasTouch: viewport.width < 768,
    isMobile: viewport.width < 768,
    deviceScaleFactor: 2,
    reducedMotion: "reduce",
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });

  await page.goto(`${base}?theme=${theme}`, { waitUntil: "networkidle" });
  await page.waitForSelector('[data-testid="channel-header-members-chip"]');

  const chip = page.locator('[data-testid="channel-header-members-chip"]');

  // Open the overlay the way a user does on this surface.
  if (surface === "hovercard") {
    await chip.hover();
  } else {
    await chip.click();
  }
  await page.waitForSelector('[data-testid="channel-agents-working-list"]', {
    timeout: 5000,
  });

  const stopCountBefore = await page
    .locator('[data-testid="channel-agents-working-stop"]')
    .count();
  if (stopCountBefore < 2) {
    fail(`${name}: expected ≥2 Working rows with Stop, got ${stopCountBefore}`);
  }
  const stopAllBefore = await page
    .locator('[data-testid="channel-agents-working-stop-all"]')
    .count();
  if (stopAllBefore !== 1) fail(`${name}: expected one Stop all, got ${stopAllBefore}`);

  // Tag the first Stop node so identity across the pending transition is a real
  // node check, not a selector re-query.
  await page.evaluate(() => {
    const first = document.querySelector('[data-testid="channel-agents-working-stop"]');
    if (!first) throw new Error("probe: no Stop button");
    first.setAttribute("data-probe-id", "stop-under-test");
    window.__probeNode = first;
  });

  // Real keyboard activation: focus, then Enter. A synthetic click would arm
  // neither `:focus-visible` nor the browser's blur-on-disable path.
  await page.locator('[data-probe-id="stop-under-test"]').focus();
  const focusedBefore = await page.evaluate(() => ({
    activeTestId: document.activeElement?.getAttribute("data-testid") ?? null,
    activeIsProbe: document.activeElement === window.__probeNode,
    tag: document.activeElement?.tagName ?? null,
    nativeDisabled: window.__probeNode?.disabled ?? null,
    ariaDisabled: window.__probeNode?.getAttribute("aria-disabled") ?? null,
    focusVisible: window.__probeNode?.matches(":focus-visible") ?? null,
  }));
  if (!focusedBefore.activeIsProbe) {
    fail(`${name}: could not focus the Stop button before activation`);
  }

  await page.keyboard.press("Enter");
  // Let React commit the pending phase and let the overlay run any dismiss.
  await page.waitForTimeout(600);

  const after = await page.evaluate(() => {
    const probe = window.__probeNode;
    return {
      phase:
        document.querySelector('[data-testid="harness-phase"]')?.textContent ?? null,
      activeTag: document.activeElement?.tagName ?? null,
      activeTestId: document.activeElement?.getAttribute("data-testid") ?? null,
      activeIsProbe: document.activeElement === probe,
      probeStillInDom: !!probe && document.contains(probe),
      overlayMounted: !!document.querySelector(
        '[data-testid="channel-agents-working-list"]',
      ),
      stopCount: document.querySelectorAll('[data-testid="channel-agents-working-stop"]')
        .length,
      stopAllReachable: !!document.querySelector(
        '[data-testid="channel-agents-working-stop-all"]',
      ),
      probeNativeDisabled: probe ? probe.disabled : null,
      probeAriaDisabled: probe ? probe.getAttribute("aria-disabled") : null,
      probeFocusVisible: probe && document.contains(probe)
        ? probe.matches(":focus-visible")
        : null,
      probeOpacity: probe ? getComputedStyle(probe).opacity : null,
      probeCursor: probe ? getComputedStyle(probe).cursor : null,
      anyNativeDisabled: [
        ...document.querySelectorAll('[data-testid="channel-agents-working-stop"]'),
        ...document.querySelectorAll('[data-testid="channel-agents-working-stop-all"]'),
      ].some((b) => b.disabled),
    };
  });

  const shot = resolve(outDir, `${name}.png`);
  await page.screenshot({ path: shot, fullPage: false });

  results.push({ name, surface, theme, viewport, focusedBefore, after, errors, shot });

  if (label === "after") {
    if (!after.overlayMounted) fail(`${name}: overlay unmounted after keyboard Stop`);
    if (!after.probeStillInDom) fail(`${name}: the Stop node left the DOM`);
    if (!after.activeIsProbe)
      fail(
        `${name}: focus left the Stop node (activeElement=${after.activeTag}/${after.activeTestId})`,
      );
    if (after.probeNativeDisabled !== false)
      fail(`${name}: Stop is natively disabled (${after.probeNativeDisabled})`);
    if (after.probeAriaDisabled !== "true")
      fail(`${name}: Stop missing aria-disabled=true (${after.probeAriaDisabled})`);
    if (!after.stopAllReachable) fail(`${name}: Stop all left the DOM`);
    if (after.stopCount !== stopCountBefore)
      fail(`${name}: row Stop count changed ${stopCountBefore} → ${after.stopCount}`);
    if (after.anyNativeDisabled) fail(`${name}: some control is still natively disabled`);
    if (!after.phase?.includes("task-1"))
      fail(`${name}: pending phase did not latch (${after.phase})`);
    if (errors.length) fail(`${name}: page errors ${JSON.stringify(errors)}`);
  }

  await context.close();
}

await browser.close();

const report = resolve(outDir, `${label}-probe.json`);
writeFileSync(report, JSON.stringify(results, null, 2));

for (const r of results) {
  console.log(
    [
      r.name.padEnd(22),
      `surface=${r.surface}`,
      `overlay=${r.after.overlayMounted}`,
      `activeElement=${r.after.activeTestId ?? r.after.activeTag}`,
      `sameNode=${r.after.activeIsProbe}`,
      `nativeDisabled=${r.after.probeNativeDisabled}`,
      `ariaDisabled=${r.after.probeAriaDisabled}`,
      `focusVisible=${r.after.probeFocusVisible}`,
      `stopRows=${r.after.stopCount}`,
      `stopAll=${r.after.stopAllReachable}`,
      `opacity=${r.after.probeOpacity}`,
    ].join("  "),
  );
}

if (label === "before") {
  const reproduced = results.filter(
    (r) => !r.after.activeIsProbe || !r.after.overlayMounted,
  );
  if (reproduced.length === 0) {
    fail(
      "before: the defect did not reproduce in any case — the BEFORE claim would be false",
    );
  }
  console.log(
    `\nBEFORE defect reproduced in ${reproduced.length}/${results.length} cases: ${reproduced
      .map((r) => r.name)
      .join(", ")}`,
  );
} else {
  console.log(`\ngate OK — ${results.length} theme/viewport runs`);
}
console.log(`probe json: ${report}`);
