#!/usr/bin/env node
/**
 * LRM-1220 landing probe — real Chromium at a real phone viewport against the
 * running worktree stack. Answers one question per run: after a COLD open of a
 * channel whose loaded page is several viewports tall, where does the viewport
 * actually land?
 *
 * Reports the scroller geometry plus the first/last message rows in view, so the
 * shot is backed by numbers instead of a visual impression. Fails if the newest
 * row is not on screen.
 *
 * Usage: node scripts/lrm1220-latest-landing-probe.mjs <label>
 * Requires /tmp/lrm1220-ctx.json from lrm1220-latest-landing-seed.mjs.
 *
 * Temporary tooling: delete once the shots are attached to LRM-1220.
 */
import { mkdirSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const label = process.argv[2] ?? "shot";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1220");
mkdirSync(outDir, { recursive: true });

const base = process.env.SHOT_BASE_URL ?? "http://localhost:13173";
const ctx = JSON.parse(readFileSync("/tmp/lrm1220-ctx.json", "utf8"));

const browser = await chromium.launch();
// 360×740 = the narrow phone the report came from (mobile Chrome/Edge).
const context = await browser.newContext({
  viewport: { width: 360, height: 740 },
  deviceScaleFactor: 2,
  isMobile: true,
  hasTouch: true,
  locale: "zh-CN",
  // Session from the seed run — the dev OTP endpoint rate-limits resends, so a
  // per-run login is not reliable. Storage state carries cookies ONLY; the
  // React Query / Virtuoso measurement caches are still cold every run.
  storageState: "/tmp/lrm1220-state.json",
});
const page = await context.newPage();
page.setDefaultTimeout(120_000);
page.setDefaultNavigationTimeout(180_000);
const consoleLines = [];
page.on("console", (m) => {
  if (m.type() === "warning" || m.type() === "error") consoleLines.push(m.text().slice(0, 200));
});

// COLD open: navigate straight to the channel, exactly like tapping a link into
// the conversation. No pre-warm visit, so Virtuoso mounts with no cached
// measurement — the state the report came from.
await page.goto(`${base}/${ctx.workspaceSlug}/channels/${ctx.chid}`, {
  waitUntil: "domcontentloaded",
});
// A first-login questionnaire sheet can cover the message list — skip it.
for (const name of [/^跳过$/, /^Skip$/i]) {
  const skip = page.getByRole("button", { name }).first();
  if (await skip.isVisible().catch(() => false)) {
    await skip.click().catch(() => {});
    await page.waitForTimeout(1500);
  }
}
const measure = () =>
  page.evaluate(() => {
    const scroller = document.querySelector('[data-testid="message-scroller"]');
    const rows = [...document.querySelectorAll('[data-testid="message-row"]')];
    const box = scroller.getBoundingClientRect();
    const visible = rows
      .filter((r) => {
        const b = r.getBoundingClientRect();
        return b.bottom > box.top + 1 && b.top < box.bottom - 1;
      })
      .map((r) => (r.innerText || "").replace(/\s+/g, " ").slice(0, 40));
    const seqOf = (t) => {
      const m = /第 (\d+) 条/.exec(t);
      return m ? Number(m[1]) : null;
    };
    return {
      scrollTop: Math.round(scroller.scrollTop),
      scrollHeight: Math.round(scroller.scrollHeight),
      clientHeight: Math.round(scroller.clientHeight),
      distanceToBottom: Math.round(
        scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight,
      ),
      renderedRows: rows.length,
      firstVisible: visible[0] ?? null,
      lastVisible: visible[visible.length - 1] ?? null,
      firstVisibleSeq: seqOf(visible[0] ?? ""),
      lastVisibleSeq: seqOf(visible[visible.length - 1] ?? ""),
      loadOlderVisible: [...document.querySelectorAll("button")].some(
        (b) =>
          /加载更早消息|Load earlier/.test(b.innerText) &&
          b.getBoundingClientRect().top > box.top - 1,
      ),
    };
  });

const settle = async () => {
  await page.waitForSelector('[data-testid="message-row"]', { timeout: 90_000 });
  // Well past the settle budget (frame cap ≈ 3s + the post-reach watch window)
  // so we measure the RESTING position, not a frame mid-settle.
  await page.waitForTimeout(9000);
};

const results = {};
await settle();
results.cold = await measure();
await page.screenshot({ path: `${outDir}/${label}-cold-360x740.png` });

// AC2 — leave the channel and come back (same session, warm caches).
await page.goto(`${base}/${ctx.workspaceSlug}/channels`, { waitUntil: "domcontentloaded" });
await page.waitForTimeout(3000);
await page.goto(`${base}/${ctx.workspaceSlug}/channels/${ctx.chid}`, {
  waitUntil: "domcontentloaded",
});
await settle();
results.reentry = await measure();
await page.screenshot({ path: `${outDir}/${label}-reentry-360x740.png` });

// AC2 — hard reload of the conversation URL.
await page.reload({ waitUntil: "domcontentloaded" });
await settle();
results.reload = await measure();
await page.screenshot({ path: `${outDir}/${label}-reload-360x740.png` });

// AC3 — 「加载更早消息」 still pages history in without breaking the view.
const older = page.getByRole("button", { name: /加载更早消息|Load earlier/ }).first();
await older.scrollIntoViewIfNeeded().catch(() => {});
await older.click({ timeout: 30_000 }).catch(() => {});
await page.waitForTimeout(6000);
results.loadOlder = {
  ...(await measure()),
  // Virtualized: only a window of rows is in the DOM, so "did older history page
  // in" is the MINIMUM rendered seq dropping below what the first page could
  // contain (the newest `PAGE_LIMIT` messages), not the presence of 第 01 条.
  minRenderedSeq: await page.evaluate(() => {
    const seqs = [...document.querySelectorAll('[data-testid="message-row"]')]
      .map((r) => /第 (\d+) 条/.exec(r.innerText || ""))
      .filter(Boolean)
      .map((m) => Number(m[1]));
    return seqs.length ? Math.min(...seqs) : null;
  }),
};
await page.screenshot({ path: `${outDir}/${label}-load-older-360x740.png` });

console.log(`[${label}] ${JSON.stringify(results, null, 2)}`);
if (consoleLines.length) console.log(`[${label}] console: ${consoleLines.join(" | ")}`);
await browser.close();

const newest = ctx.count;
const failures = [];
for (const phase of ["cold", "reentry", "reload"]) {
  if (results[phase].lastVisibleSeq !== newest) {
    failures.push(
      `${phase}: newest row (第 ${newest} 条) NOT in view — shows 第 ${results[phase].firstVisibleSeq} 条 … 第 ${results[phase].lastVisibleSeq} 条 (scrollTop ${results[phase].scrollTop})`,
    );
  }
}
// The first page is the newest PAGE_LIMIT messages, so anything older than this
// seq can only be in the DOM because 「加载更早消息」 paged it in.
const PAGE_LIMIT = Number(process.env.SEED_PAGE_LIMIT ?? 50);
const oldestOnFirstPage = Math.max(1, newest - PAGE_LIMIT + 1);
if (!(results.loadOlder.minRenderedSeq < oldestOnFirstPage)) {
  failures.push(
    `loadOlder: 「加载更早消息」 paged nothing older in — min rendered 第 ${results.loadOlder.minRenderedSeq} 条, first page starts at 第 ${oldestOnFirstPage} 条`,
  );
}
if (failures.length) {
  console.error(`[${label}] FAIL\n- ${failures.join("\n- ")}`);
  process.exitCode = 1;
} else {
  console.log(`[${label}] PASS — cold / re-entry / reload all land on 第 ${newest} 条; load-older still works`);
}
