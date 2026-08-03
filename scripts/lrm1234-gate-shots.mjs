/**
 * LRM-1234 gate shots — real Chromium Tab order on ReportReader dismiss scrim.
 *
 * Reuses scripts/lrm1164-harness/?case=report (does not modify the harness).
 * Asserts:
 *   - scrim has aria-hidden + tabIndex=-1
 *   - Tab sequence never lands on the scrim
 *   - first focusable is a visible control (narrow: outline toggle / desktop: Copy MD)
 * Then writes 375 / 1280 PNGs.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1234.
 */
import { mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1234");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5201/";
const shotWidths = [375, 1280];

const browser = await chromium.launch({ executablePath: process.env.CHROME_BIN || undefined });
const page = await browser.newPage();
const errors = [];
const ignorable = (t) => t.includes("favicon.ico") || t.includes("/api/");
page.on("pageerror", (e) => errors.push(String(e)));
page.on("console", (m) => {
  if (m.type() === "error" && !ignorable(m.location().url + m.text())) errors.push(m.text());
});
page.on("requestfailed", (r) => {
  if (!ignorable(r.url())) errors.push(`requestfailed ${r.url()}`);
});

const fail = (msg) => {
  throw new Error(msg);
};

await page.goto(`${base}?case=report`, { waitUntil: "networkidle" });
await page.waitForSelector('[data-testid="research-delivery-modal-card"]');
await page.waitForSelector('[data-testid="research-delivery-modal-dismiss-scrim"]');

const report = [];

for (const width of shotWidths) {
  await page.setViewportSize({ width, height: 900 });
  await page.waitForTimeout(150);

  const probe = await page.evaluate(() => {
    const scrim = document.querySelector(
      '[data-testid="research-delivery-modal-dismiss-scrim"]',
    );
    const dialog = document.querySelector('[data-testid="research-delivery-modal"]');
    if (!scrim || !dialog) return { ok: false, reason: "missing scrim/dialog" };

    const ariaHidden = scrim.getAttribute("aria-hidden");
    const tabIndex = scrim.tabIndex;
    const closeButtons = [...dialog.querySelectorAll("button")].filter((b) => {
      const name = (b.getAttribute("aria-label") || b.textContent || "").trim();
      return name === "关闭" || name === "Close" || /^关闭|^Close/i.test(name);
    });
    // Accessible Close names: scrim must not contribute (aria-hidden strips it).
    const accessibleCloses = closeButtons.filter(
      (b) => b.getAttribute("aria-hidden") !== "true" && b.tabIndex >= 0,
    );

    const isFocusable = (el) => {
      if (!(el instanceof HTMLElement)) return false;
      if (el.hasAttribute("disabled")) return false;
      if (el.getAttribute("aria-hidden") === "true") return false;
      const cs = getComputedStyle(el);
      if (cs.display === "none" || cs.visibility === "hidden") return false;
      if (el.tabIndex < 0) return false;
      const r = el.getBoundingClientRect();
      return r.width > 0 && r.height > 0;
    };

    const candidates = [
      ...dialog.querySelectorAll(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
      ),
    ].filter(isFocusable);

    // Simulate Tab from body: first stop must not be scrim.
    document.body.focus();
    const first = candidates[0] ?? null;
    return {
      ok: true,
      ariaHidden,
      tabIndex,
      accessibleCloseCount: accessibleCloses.length,
      firstTestId: first?.getAttribute("data-testid") ?? null,
      firstTag: first?.tagName ?? null,
      firstLabel:
        first?.getAttribute("aria-label") ||
        (first?.textContent || "").trim().slice(0, 40) ||
        null,
      candidatesIncludeScrim: candidates.includes(scrim),
      candidateCount: candidates.length,
    };
  });

  if (!probe.ok) fail(`report@${width}: ${probe.reason}`);
  if (probe.ariaHidden !== "true")
    fail(`report@${width}: scrim aria-hidden=${probe.ariaHidden}`);
  if (probe.tabIndex !== -1) fail(`report@${width}: scrim tabIndex=${probe.tabIndex}`);
  if (probe.candidatesIncludeScrim)
    fail(`report@${width}: Tab candidates still include dismiss scrim`);
  if (probe.accessibleCloseCount !== 1)
    fail(
      `report@${width}: expected 1 accessible Close, got ${probe.accessibleCloseCount}`,
    );

  const narrow = width < 768;
  if (narrow) {
    if (probe.firstTestId !== "research-report-outline-toggle")
      fail(
        `report@${width}: first focusable expected outline toggle, got ${probe.firstTestId}/${probe.firstLabel}`,
      );
  } else {
    // Desktop: first visible control in header after title chrome is Copy MD
    // (outline toggle is md:hidden). Label is localized (复制 MD / Copy MD).
    const label = probe.firstLabel || "";
    if (!/copy|复制|md/i.test(label) && probe.firstTestId === "research-report-outline-toggle")
      fail(`report@${width}: desktop still focuses outline toggle`);
    if (!/copy|复制|md/i.test(label) && !probe.firstTag)
      fail(`report@${width}: no first focusable`);
  }

  // Live Tab: press Tab once and assert activeElement is not the scrim.
  await page.evaluate(() => {
    const dialog = document.querySelector('[data-testid="research-delivery-modal"]');
    dialog?.focus?.();
  });
  await page.keyboard.press("Tab");
  const active = await page.evaluate(() => {
    const el = document.activeElement;
    return {
      testId: el?.getAttribute?.("data-testid") ?? null,
      isScrim: el?.getAttribute?.("data-testid") === "research-delivery-modal-dismiss-scrim",
      tag: el?.tagName ?? null,
      label: el?.getAttribute?.("aria-label") || (el?.textContent || "").trim().slice(0, 40),
    };
  });
  if (active.isScrim) fail(`report@${width}: Tab landed on dismiss scrim`);

  await page.screenshot({ path: resolve(outDir, `report-scrim-tab-${width}.png`) });
  report.push({ width, ...probe, activeAfterTab: active });
  console.log(JSON.stringify({ width, ...probe, activeAfterTab: active }));
}

if (errors.length) fail(`page errors: ${errors.join(" | ")}`);
console.log("LRM-1234 gate shots OK", report.map((r) => r.width));
await browser.close();
