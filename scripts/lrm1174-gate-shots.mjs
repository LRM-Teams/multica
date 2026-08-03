/**
 * LRM-1174 gate shots — real Chromium, real `<dialog>.showModal()` top layer.
 *
 * Cell A of the LRM-1173 freeze: a touch laptop in a narrow window reports
 * `(pointer: fine)` AND `innerWidth < 768`. The JS gate opens the sheet; on
 * origin/dev the CSS then hid it → invisible modal, inert page, app looks dead.
 *
 * The probe:
 *   1. asserts the harness class strings ARE the real ones (AFTER === working
 *      tree component, BEFORE === origin/dev component) so the shots cannot
 *      describe a fiction;
 *   2. measures paint (`display`) and inertness (background button clickable?)
 *      for both gates at 760px, the dead-zone width;
 *   3. writes one PNG per gate.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1174.
 */
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1174");
mkdirSync(outDir, { recursive: true });

const COMPONENT = "packages/views/channels/components/channel-message-bubble.tsx";
const HARNESS = resolve(root, "scripts/lrm1174-harness/main.tsx");
const DIALOG_CLASS_RE = /className="(fixed inset-0 z-50 m-0 h-dvh[^"]*)"/g;

const dialogClassesIn = (source) => [...source.matchAll(DIALOG_CLASS_RE)].map((m) => m[1]);
const harnessConst = (name) => {
  const match = new RegExp(`${name} =\\s*\\n?\\s*"([^"]+)"`).exec(readFileSync(HARNESS, "utf8"));
  if (!match) throw new Error(`harness is missing ${name}`);
  return match[1];
};

// 1 — source-truth assertions.
const afterClasses = dialogClassesIn(readFileSync(resolve(root, COMPONENT), "utf8"));
const beforeClasses = dialogClassesIn(
  execFileSync("git", ["show", `origin/dev:${COMPONENT}`], { cwd: root, encoding: "utf8" }),
);
if (afterClasses.length !== 2) throw new Error(`expected 2 sheet dialogs, found ${afterClasses.length}`);
if (beforeClasses.length !== 2) throw new Error(`origin/dev sheet dialogs not found`);
if (new Set(afterClasses).size !== 1 || new Set(beforeClasses).size !== 1)
  throw new Error("the two sheets no longer share one class string — update the probe");
const [afterClass] = afterClasses;
const [beforeClass] = beforeClasses;
if (afterClass !== harnessConst("AFTER_DIALOG_CLASS"))
  throw new Error(`harness AFTER drifted from ${COMPONENT}:\n  ${afterClass}`);
if (beforeClass !== harnessConst("BEFORE_DIALOG_CLASS"))
  throw new Error(`harness BEFORE drifted from origin/dev:\n  ${beforeClass}`);
if (afterClass.includes("[@media(pointer:fine)]:hidden"))
  throw new Error("working tree still hides the sheet from fine pointers");
if (!beforeClass.includes("[@media(pointer:fine)]:hidden"))
  throw new Error("origin/dev has no fine:hidden — nothing to prove");

const url = process.env.HARNESS_URL ?? "http://localhost:5203/";
const browser = await chromium.launch({ executablePath: process.env.CHROME_BIN || undefined });
// Default desktop Chromium = a mouse device: `(pointer: fine)` matches. 760px
// puts it inside the 640–767 dead zone the freeze names.
const page = await browser.newPage({ viewport: { width: 760, height: 720 } });
const errors = [];
page.on("pageerror", (e) => errors.push(String(e)));
page.on("console", (m) => {
  if (m.type() === "error" && !m.location().url.includes("favicon")) errors.push(m.text());
});

const report = [];
for (const gate of ["before", "after"]) {
  await page.goto(`${url}?gate=${gate}`, { waitUntil: "networkidle" });
  const sheet = page.locator('[data-testid="mobile-sheet"]');
  await sheet.waitFor({ state: "attached" });

  const media = await page.evaluate(() => ({
    pointerFine: window.matchMedia("(pointer: fine)").matches,
    narrow: window.matchMedia("(max-width: 767px)").matches,
    innerWidth: window.innerWidth,
  }));
  if (!media.pointerFine || !media.narrow)
    throw new Error(`probe viewport is not the dead-zone cell: ${JSON.stringify(media)}`);

  const paint = await sheet.evaluate((el) => {
    const style = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    return {
      open: el.open,
      display: style.display,
      visibility: style.visibility,
      paintedHeight: Math.round(rect.height),
    };
  });

  // Inertness: a modal dialog makes the page behind it unclickable. Combined
  // with display:none that is exactly the "app is frozen" report.
  const backgroundClicked = await page.evaluate(async () => {
    delete document.body.dataset.backgroundClicked;
    const target = document.querySelector('[data-testid="background-target"]');
    const box = target.getBoundingClientRect();
    const hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
    return { reachable: hit === target || target.contains(hit), clicked: false };
  });

  report.push({
    gate,
    ...media,
    ...paint,
    backgroundReachable: backgroundClicked.reachable,
    verdict:
      paint.display === "none" && paint.open
        ? "DEAD: modal open (page inert) but nothing painted"
        : "OK: modal open and painted",
  });

  await page.screenshot({ path: resolve(outDir, `mixed-pointer-760--${gate}.png`) });
}

const before = report.find((r) => r.gate === "before");
const after = report.find((r) => r.gate === "after");
if (before.display !== "none") throw new Error("BEFORE did not reproduce the invisible modal");
if (after.display === "none") throw new Error("AFTER still hides the sheet");
if (after.paintedHeight <= 0) throw new Error("AFTER painted nothing");
if (before.backgroundReachable) throw new Error("BEFORE page was not inert — no dead zone to fix");
if (errors.length) throw new Error(`console/page errors: ${errors.join(" | ")}`);

console.log(JSON.stringify(report, null, 2));
console.log("shots ->", outDir);
await browser.close();
