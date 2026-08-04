#!/usr/bin/env node
/**
 * LRM-1296 fixture — a workspace with enough channels/DM rows that the
 * per-switch `GET /api/channels` + `GET /api/dms` refetches are real work, and
 * three message-heavy channels to switch between.
 *
 * Real API through a real logged-in browser session (no direct SQL).
 * Writes /tmp/lrm1296-ctx.json + /tmp/lrm1296-state.json.
 *
 * Temporary tooling: delete once the probe output is attached to LRM-1296.
 */
import { writeFileSync } from "node:fs";
import { chromium } from "playwright";

const base = process.env.SHOT_BASE_URL ?? "http://localhost:13323";
const email = process.env.SHOT_EMAIL ?? "frank@lrm1296.test";
const SWITCH_CHANNELS = 3;
const FILLER_CHANNELS = Number(process.env.SEED_FILLER ?? 12);
const MESSAGES = Number(process.env.SEED_COUNT ?? 60);

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" });
page.setDefaultTimeout(120_000);
page.setDefaultNavigationTimeout(180_000);

await page.goto(`${base}/login`, { waitUntil: "domcontentloaded" });
await page.waitForTimeout(4000);
await page.locator("input").first().fill(email);
const cont = page.getByRole("button", { name: /继续|Continue/ }).first();
if (await cont.isVisible().catch(() => false)) await cont.click();
else await page.keyboard.press("Enter");
await page
  .locator('input[autocomplete="one-time-code"], input[maxlength="6"]')
  .first()
  .waitFor({ state: "visible", timeout: 60_000 });
const otp = page.locator('input[autocomplete="one-time-code"], input[maxlength="6"]').first();
await otp.click();
await otp.fill("888888");

const api = page.request;
let authed = false;
let wsBody = [];
for (let attempt = 0; attempt < 20 && !authed; attempt += 1) {
  await page.waitForTimeout(1500);
  const probe = await api.get(`${base}/api/workspaces`);
  if (probe.ok()) {
    wsBody = await probe.json();
    authed = true;
  }
}
if (!authed) throw new Error(`login never authenticated (url=${page.url()})`);

const csrf = async () => {
  const cookies = await page.context().cookies();
  const c = cookies.find((x) => x.name === "multica_csrf");
  if (!c) throw new Error("multica_csrf cookie missing");
  return c.value;
};

const workspaces = Array.isArray(wsBody) ? wsBody : (wsBody.workspaces ?? wsBody.items ?? []);
let ws = workspaces[0];
if (!ws) {
  const slug = `lrm1296-${Date.now().toString(36)}`;
  const created = await api.post(`${base}/api/workspaces`, {
    headers: { "Content-Type": "application/json", "X-CSRF-Token": await csrf() },
    data: { name: "LRM-1296", slug, issue_prefix: "L96" },
  });
  if (!created.ok()) throw new Error(`POST /api/workspaces ${created.status()} ${await created.text()}`);
  ws = await created.json();
}
const headers = {
  "Content-Type": "application/json",
  "X-CSRF-Token": await csrf(),
  "X-Workspace-ID": ws.id,
};

async function createChannel(name) {
  const res = await api.post(`${base}/api/channels`, { headers, data: { name } });
  if (!res.ok()) throw new Error(`POST /api/channels ${res.status()} ${await res.text()}`);
  const body = await res.json();
  return body.id ?? body.channel?.id;
}

const stamp = Date.now().toString(36);
const switchTargets = [];
for (let i = 0; i < SWITCH_CHANNELS; i += 1) {
  const name = `l96-switch-${i + 1}-${stamp}`;
  const id = await createChannel(name);
  for (let m = 1; m <= MESSAGES; m += 1) {
    const res = await api.post(`${base}/api/channels/${id}/messages`, {
      headers,
      data: { content: `#${name} 第 ${String(m).padStart(2, "0")} 条 — 切换感知延迟固定文案，够长以撑满视口。`.repeat(m % 3 === 0 ? 2 : 1) },
    });
    if (!res.ok()) throw new Error(`POST message ${m} ${res.status()} ${await res.text()}`);
  }
  switchTargets.push({ id, name });
}

// Filler rows so the sidebar list payloads (per-row unread aggregate +
// last_message enrichment) are not a trivial 3-row query.
for (let i = 0; i < FILLER_CHANNELS; i += 1) {
  const name = `l96-filler-${i + 1}-${stamp}`;
  const id = await createChannel(name);
  await api.post(`${base}/api/channels/${id}/messages`, {
    headers,
    data: { content: `#${name} 侧栏填充行。` },
  });
}

await page.context().storageState({ path: "/tmp/lrm1296-state.json" });
writeFileSync(
  "/tmp/lrm1296-ctx.json",
  JSON.stringify({ email, workspaceId: ws.id, workspaceSlug: ws.slug, switchTargets, messages: MESSAGES, filler: FILLER_CHANNELS }, null, 2),
);
await browser.close();
console.log(`seeded ${SWITCH_CHANNELS}×${MESSAGES} msgs + ${FILLER_CHANNELS} filler channels in ${ws.slug}`);
