#!/usr/bin/env node
/**
 * LRM-1296 probe — what the network actually does on a channel switch.
 *
 * Measures, per switch, on the real logged-in stack:
 *   listRefetch   — `GET /api/channels` + `GET /api/dms` full-list refetches
 *                   triggered by the entry read receipt
 *   aborted       — requests for the channel the user just LEFT that got
 *                   cancelled (net::ERR_ABORTED) instead of running to completion
 *   staleFinished — requests for a LEFT channel that still completed
 *   msgMs         — click → `GET /api/channels/<target>/messages` response
 *
 * Run once on origin/dev (BEFORE) and once on the fix (AFTER) against the same
 * seed. Temporary tooling: delete once the numbers are attached to LRM-1296.
 */
import { readFileSync, writeFileSync } from "node:fs";
import { chromium } from "playwright";

const base = process.env.SHOT_BASE_URL ?? "http://localhost:13323";
const label = process.env.PROBE_LABEL ?? "run";
const ctx = JSON.parse(readFileSync("/tmp/lrm1296-ctx.json", "utf8"));
const targets = ctx.switchTargets;

const browser = await chromium.launch();
const page = await browser.newPage({
  viewport: { width: 1440, height: 900 },
  locale: "zh-CN",
  storageState: "/tmp/lrm1296-state.json",
});
page.setDefaultTimeout(120_000);

const events = [];
const idOf = (url) => {
  const m = url.match(/\/api\/channels\/([0-9a-f-]{36})/);
  return m ? m[1] : null;
};
const isList = (url) =>
  /\/api\/channels(\?|$)/.test(url.replace(/^https?:\/\/[^/]+/, "")) ||
  /\/api\/dms(\?|$)/.test(url.replace(/^https?:\/\/[^/]+/, ""));

page.on("request", (r) => {
  const u = r.url();
  if (!u.includes("/api/")) return;
  events.push({ kind: "start", t: Date.now(), url: u, method: r.method(), cid: idOf(u), list: isList(u) });
});
page.on("response", (r) => {
  const u = r.url();
  if (!u.includes("/api/")) return;
  events.push({ kind: "done", t: Date.now(), url: u, status: r.status(), cid: idOf(u), list: isList(u) });
});
page.on("requestfailed", (r) => {
  const u = r.url();
  if (!u.includes("/api/")) return;
  events.push({ kind: "failed", t: Date.now(), url: u, failure: r.failure()?.errorText, cid: idOf(u), list: isList(u) });
});

await page.goto(`${base}/${ctx.workspaceSlug}/channels`, { waitUntil: "domcontentloaded" });
await page.waitForTimeout(9000);

async function openSidebarRow(name) {
  const row = page.getByText(name, { exact: false }).first();
  await row.waitFor({ state: "visible", timeout: 60_000 });
  await row.click();
}

// Warm the shell + land on the first target so the measured switches are
// switches, not a cold app boot.
await openSidebarRow(targets[0].name);
await page.waitForTimeout(6000);

const results = [];
// Fast A→B→C: each switch fires while the previous channel's reads are still
// in flight — the case the user feels as "切换卡".
for (let i = 1; i < targets.length; i += 1) {
  const from = targets[i - 1];
  const to = targets[i];
  const t0 = Date.now();
  events.length = 0;
  await openSidebarRow(to.name);
  await page.waitForTimeout(4000);

  const window = events.filter((e) => e.t >= t0);
  const listRefetch = window.filter((e) => e.kind === "start" && e.list && e.method === "GET").length;
  const aborted = window.filter(
    (e) => e.kind === "failed" && e.cid === from.id && /ABORT/i.test(e.failure ?? ""),
  ).length;
  const staleFinished = window.filter((e) => e.kind === "done" && e.cid === from.id).length;
  const msgDone = window.find(
    (e) => e.kind === "done" && e.cid === to.id && /\/messages\?/.test(e.url),
  );
  results.push({
    switch: `${from.name} → ${to.name}`,
    listRefetch,
    aborted,
    staleFinished,
    apiRequests: window.filter((e) => e.kind === "start").length,
    msgMs: msgDone ? msgDone.t - t0 : null,
  });
  // Switch back so the next iteration starts from a comparable state.
  await page.waitForTimeout(1500);
}

writeFileSync(`/tmp/lrm1296-${label}-deliberate.json`, JSON.stringify(results, null, 2));

// ── Phase 2: rapid burst under throttling ────────────────────────────────────
// Switch again before the previous channel's reads land — the case where the
// abandoned requests either get cancelled or keep holding connection slots.
const cdp = await page.context().newCDPSession(page);
await cdp.send("Network.enable");
await cdp.send("Network.emulateNetworkConditions", {
  offline: false,
  latency: 400,
  downloadThroughput: (500 * 1024) / 8,
  uploadThroughput: (500 * 1024) / 8,
});

await openSidebarRow(targets[0].name);
await page.waitForTimeout(8000);

const leftIds = targets.slice(0, -1).map((t) => t.id);
const final = targets[targets.length - 1];
const bursts = [];
for (let round = 0; round < 3; round += 1) {
  await openSidebarRow(targets[0].name);
  await page.waitForTimeout(6000);
  events.length = 0;
  const burstStart = Date.now();
  for (let i = 1; i < targets.length; i += 1) {
    await openSidebarRow(targets[i].name);
    await page.waitForTimeout(150);
  }
  await page.waitForTimeout(15000);
  const burst = events.filter((e) => e.t >= burstStart);
  const done = burst.find((e) => e.kind === "done" && e.cid === final.id && /\/messages\?/.test(e.url));
  bursts.push({
    round: round + 1,
    apiRequests: burst.filter((e) => e.kind === "start").length,
    listRefetch: burst.filter((e) => e.kind === "start" && e.list && e.method === "GET").length,
    abortedStale: burst.filter(
      (e) => e.kind === "failed" && leftIds.includes(e.cid) && /ABORT/i.test(e.failure ?? ""),
    ).length,
    staleFinished: burst.filter((e) => e.kind === "done" && leftIds.includes(e.cid)).length,
    finalMessagesMs: done ? done.t - burstStart : null,
  });
}

const burstSummary = {
  route: targets.map((t) => t.name).join(" → "),
  gapMs: 150,
  throttle: "latency 400ms / 500kbps",
  rounds: bursts,
};

writeFileSync(
  `/tmp/lrm1296-${label}.json`,
  JSON.stringify({ deliberate: results, burst: burstSummary }, null, 2),
);
await page.screenshot({ path: `/tmp/lrm1296-${label}.png` });
await browser.close();
console.log(`[${label}] deliberate switches`);
for (const r of results) {
  console.log(
    `  ${r.switch}\n    listRefetch=${r.listRefetch} abortedStale=${r.aborted} staleFinished=${r.staleFinished} apiRequests=${r.apiRequests} msgMs=${r.msgMs}`,
  );
}
console.log(`[${label}] rapid burst (${burstSummary.throttle}, gap ${burstSummary.gapMs}ms) — ${burstSummary.route}`);
for (const r of bursts) {
  console.log(
    `  round${r.round}: apiRequests=${r.apiRequests} listRefetch=${r.listRefetch} abortedStale=${r.abortedStale} staleFinished=${r.staleFinished} finalMessagesMs=${r.finalMessagesMs}`,
  );
}
