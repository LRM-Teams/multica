/**
 * LRM-1298 gate shots + real-Chromium focus assertions.
 *
 * Presses REAL Tab keys (which jsdom cannot do) against both variants:
 *   before = origin/dev frame verbatim, no focus management
 *   after  = real AttachmentPreviewModal with the focus contract
 *
 * Per variant/theme/viewport it records:
 *   1. where focus lands on open,
 *   2. the element focused after 8 consecutive real Tab presses,
 *   3. whether focus ever escaped the aria-modal dialog,
 *   4. where focus lands after Escape closes the dialog.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1298.
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1298");
mkdirSync(outDir, { recursive: true });

const base = process.env.HARNESS_URL ?? "http://localhost:5298/";
const viewports = [
  { name: "1440", width: 1440, height: 900 },
  { name: "360", width: 360, height: 780 },
];
const themes = ["light", "dark"];
const variants = ["before", "after"];

const describeActive = () =>
  // eslint-disable-next-line no-undef
  ({
    tag: document.activeElement?.tagName ?? null,
    id: document.activeElement?.id || null,
    label:
      document.activeElement?.getAttribute("aria-label") ||
      document.activeElement?.getAttribute("role") ||
      (document.activeElement?.textContent || "").trim().slice(0, 24) ||
      null,
    insideDialog: (() => {
      const dialog = document.querySelector('[role="dialog"]');
      const el = document.activeElement;
      return !!dialog && !!el && dialog.contains(el);
    })(),
    isBody: document.activeElement === document.body,
  });

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});

const errors = [];
const report = [];

for (const vp of viewports) {
  const page = await browser.newPage({
    viewport: { width: vp.width, height: vp.height },
  });
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("console", (m) => {
    if (
      m.type() === "error" &&
      !m.text().includes("favicon") &&
      !m.text().includes("404 (Not Found)")
    ) {
      errors.push(m.text());
    }
  });

  for (const theme of themes) {
    for (const variant of variants) {
      const url = `${base}?variant=${variant}&theme=${theme}`;
      await page.goto(url, { waitUntil: "load" });
      await page.waitForSelector('[role="dialog"]');
      await page.waitForTimeout(250);

      const onOpen = await page.evaluate(describeActive);
      await page.screenshot({
        path: resolve(outDir, `${variant}-${theme}-${vp.name}-01-open.png`),
      });

      // 8 real Tab presses. A trapped dialog can never report insideDialog=false.
      const walk = [];
      for (let i = 0; i < 8; i += 1) {
        await page.keyboard.press("Tab");
        walk.push(await page.evaluate(describeActive));
      }
      await page.screenshot({
        path: resolve(
          outDir,
          `${variant}-${theme}-${vp.name}-02-after-8-tabs.png`,
        ),
      });

      const escaped = walk.some((step) => !step.insideDialog);

      // Shift+Tab back off the first control — the other escape direction.
      await page.keyboard.press("Shift+Tab");
      const afterShiftTab = await page.evaluate(describeActive);

      await page.keyboard.press("Escape");
      await page.waitForTimeout(200);
      const afterClose = await page.evaluate(describeActive);
      await page.screenshot({
        path: resolve(
          outDir,
          `${variant}-${theme}-${vp.name}-03-after-escape.png`,
        ),
      });

      report.push({
        variant,
        theme,
        viewport: vp.name,
        onOpen,
        tabWalk: walk,
        escapedDialogDuringTabWalk: escaped,
        afterShiftTab,
        afterClose,
        restoredToTrigger: afterClose.id === "preview-trigger",
      });
    }
  }

  await page.close();
}

// Extra pass (1440/light only): the trigger is unmounted while the modal is
// open and returns as a different DOM node. Restoring by node identity alone
// silently no-ops here, so this is what the id/data-testid re-locator buys.
const unmountPage = await browser.newPage({
  viewport: { width: 1440, height: 900 },
});
unmountPage.on("pageerror", (e) => errors.push(String(e)));
await unmountPage.goto(
  `${base}?variant=after&theme=light&unmountTrigger=1`,
  { waitUntil: "load" },
);
await unmountPage.waitForSelector('[role="dialog"]');
await unmountPage.waitForTimeout(250);
const unmountOnOpen = await unmountPage.evaluate(describeActive);
const triggerGoneWhileOpen = await unmountPage.evaluate(
  // eslint-disable-next-line no-undef
  () => document.getElementById("preview-trigger") === null,
);
await unmountPage.screenshot({
  path: resolve(outDir, "after-light-1440-04-unmounted-trigger-open.png"),
});
await unmountPage.keyboard.press("Escape");
await unmountPage.waitForTimeout(250);
const unmountAfterClose = await unmountPage.evaluate(describeActive);
await unmountPage.screenshot({
  path: resolve(outDir, "after-light-1440-05-unmounted-trigger-restored.png"),
});
await unmountPage.close();

const unmountRow = {
  variant: "after",
  theme: "light",
  viewport: "1440",
  case: "trigger unmounted while open (LRM-1177 shape)",
  onOpen: unmountOnOpen,
  triggerGoneWhileOpen,
  afterClose: unmountAfterClose,
  restoredToTrigger: unmountAfterClose.id === "preview-trigger",
};
report.push(unmountRow);

await browser.close();

writeFileSync(
  resolve(outDir, "focus-report.json"),
  `${JSON.stringify({ errors, report }, null, 2)}\n`,
);

const afterRows = report.filter((r) => r.variant === "after" && !r.case);
const beforeRows = report.filter((r) => r.variant === "before");

const failures = [];
for (const row of afterRows) {
  const at = `${row.variant}/${row.theme}/${row.viewport}`;
  if (!row.onOpen.insideDialog) failures.push(`${at}: open did not focus into the dialog`);
  if (row.escapedDialogDuringTabWalk) failures.push(`${at}: Tab escaped the dialog`);
  if (!row.afterShiftTab.insideDialog) failures.push(`${at}: Shift+Tab escaped the dialog`);
  if (!row.restoredToTrigger) failures.push(`${at}: focus not restored to the trigger`);
}
// The BEFORE variant must actually reproduce the defect, otherwise the AFTER
// evidence proves nothing. Note: `restoredToTrigger` is meaningless for BEFORE
// — focus never leaves the trigger in the first place, which IS the defect.
if (!beforeRows.some((r) => r.escapedDialogDuringTabWalk)) {
  failures.push("before: Tab never escaped — defect not reproduced");
}
if (beforeRows.some((r) => r.onOpen.insideDialog)) {
  failures.push("before: open focused into the dialog — defect not reproduced");
}
if (!unmountRow.restoredToTrigger) {
  failures.push(
    "after/unmounted-trigger: focus not restored to the re-created trigger",
  );
}

console.log(JSON.stringify({ errors, report }, null, 2));
console.log(
  failures.length ? `FAIL\n${failures.join("\n")}` : "PASS — focus contract holds in real Chromium",
);
process.exit(failures.length || errors.length ? 1 : 0);
