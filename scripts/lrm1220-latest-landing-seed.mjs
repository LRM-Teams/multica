#!/usr/bin/env node
/**
 * LRM-1220 fixture — a channel whose loaded page is many viewports tall, so the
 * cold-open landing position is observable (the bug: the viewport lands on the
 * OLDEST loaded row, "今天" + 当日第一条, instead of the newest).
 *
 * Real API through a real logged-in browser session (no direct SQL): login →
 * workspace → create channel → post N messages. Writes /tmp/lrm1220-ctx.json.
 *
 * Temporary tooling: delete once the shots are attached to LRM-1220.
 */
import { writeFileSync } from "node:fs";
import { chromium } from "playwright";

const base = process.env.SHOT_BASE_URL ?? "http://localhost:13173";
const email = process.env.SHOT_EMAIL ?? "frank@lrm1220.test";
const COUNT = Number(process.env.SEED_COUNT ?? 60);

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
// Wait for the OTP step: one input with maxLength 6 / autocomplete=one-time-code.
await page
  .locator('input[autocomplete="one-time-code"], input[maxlength="6"]')
  .first()
  .waitFor({ state: "visible", timeout: 60_000 });
// Dev verification code (MULTICA_DEV_VERIFICATION_CODE); the OTP field
// auto-submits once six characters land.
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
if (!authed) {
  await page.screenshot({ path: "/tmp/lrm1220-login-DEBUG.png" });
  throw new Error(
    `login never authenticated (url=${page.url()}): ` +
      (await page.locator("body").innerText().catch(() => "")).slice(0, 500),
  );
}

const csrf = async () => {
  const cookies = await page.context().cookies();
  const c = cookies.find((x) => x.name === "multica_csrf");
  if (!c) throw new Error("multica_csrf cookie missing — not logged in?");
  return c.value;
};
const writeHeaders = async (extra = {}) => ({
  "Content-Type": "application/json",
  "X-CSRF-Token": await csrf(),
  ...extra,
});

const workspaces = Array.isArray(wsBody) ? wsBody : (wsBody.workspaces ?? wsBody.items ?? []);
let ws = workspaces[0];
if (!ws) {
  // A brand-new dev account has no workspace yet (onboarding normally creates
  // one) — bootstrap it through the same API the product uses.
  const slug = `lrm1220-${Date.now().toString(36)}`;
  const created = await api.post(`${base}/api/workspaces`, {
    headers: await writeHeaders(),
    data: { name: "LRM-1220", slug, issue_prefix: "L12" },
  });
  if (!created.ok()) throw new Error(`POST /api/workspaces ${created.status()} ${await created.text()}`);
  ws = await created.json();
}
if (!ws?.id) throw new Error(`no workspace for ${email}: ${JSON.stringify(ws).slice(0, 400)}`);
const headers = await writeHeaders({ "X-Workspace-ID": ws.id });

const name = `lrm1220-${Date.now().toString(36)}`;
const chRes = await api.post(`${base}/api/channels`, { headers, data: { name } });
if (!chRes.ok()) throw new Error(`POST /api/channels ${chRes.status()} ${await chRes.text()}`);
const channel = await chRes.json();
const chid = channel.id ?? channel.channel?.id;
if (!chid) throw new Error(`no channel id: ${JSON.stringify(channel).slice(0, 400)}`);

for (let i = 1; i <= COUNT; i += 1) {
  const res = await api.post(`${base}/api/channels/${chid}/messages`, {
    headers,
    data: {
      content:
        `第 ${String(i).padStart(2, "0")} 条 — ` +
        "进群应该落在最新一条，不是今天第一条。".repeat(i % 3 === 0 ? 2 : 1),
    },
  });
  if (!res.ok()) throw new Error(`POST message ${i} ${res.status()} ${await res.text()}`);
}

// Persist the session so the probe can reuse it: the dev OTP endpoint has a
// ~60s resend cooldown, so a fresh login per probe run is not reliable.
await page.context().storageState({ path: "/tmp/lrm1220-state.json" });
const ctx = { email, workspaceId: ws.id, workspaceSlug: ws.slug, chid, name, count: COUNT };
writeFileSync("/tmp/lrm1220-ctx.json", JSON.stringify(ctx, null, 2));
await browser.close();
console.log(`seeded ${COUNT} messages into #${name} (${chid}) of ${ws.slug}`);
