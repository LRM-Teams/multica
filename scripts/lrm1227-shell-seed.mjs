#!/usr/bin/env node
/**
 * LRM-1227 fixture — one channel that contains every row shape the LRM-1233
 * status matrix names, so the joined bubble shell can be photographed instead of
 * described: a 3-message same-author group, a second author's group, a
 * cross-day pair (date divider adjacency), a peer message that @-mentions the
 * viewer (self-mention wash must replace the shell fill), a system row and a
 * tombstone (must stay standalone, never joined).
 *
 * Login + channel + the viewer's own messages go through the real API. The peer
 * author, the back-dated rows, the system row and the tombstone are inserted
 * directly (a single dev session cannot author them), which is fixture data
 * only — every class under test is still produced by the real component.
 *
 * Temporary tooling: delete once the shots are attached to LRM-1227.
 */
import { writeFileSync } from "node:fs";
import { chromium } from "playwright";

const base = process.env.SHOT_BASE_URL ?? "http://localhost:3000";
const email = process.env.SHOT_EMAIL ?? "frank@lrm1227.test";

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
const otp = page.locator('input[autocomplete="one-time-code"], input[maxlength="6"]').first();
await otp.waitFor({ state: "visible", timeout: 60_000 });
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
  await page.screenshot({ path: "/tmp/lrm1227-login-DEBUG.png" });
  throw new Error(`login never authenticated (url=${page.url()})`);
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
  const slug = `lrm1227-${Date.now().toString(36)}`;
  const created = await api.post(`${base}/api/workspaces`, {
    headers: await writeHeaders(),
    data: { name: "LRM-1227", slug, issue_prefix: "L27" },
  });
  if (!created.ok()) throw new Error(`POST /api/workspaces ${created.status()} ${await created.text()}`);
  ws = await created.json();
}
if (!ws?.id) throw new Error(`no workspace: ${JSON.stringify(ws).slice(0, 400)}`);
const headers = await writeHeaders({ "X-Workspace-ID": ws.id });

const me = await (await api.get(`${base}/api/me`, { headers })).json().catch(() => null);
const myId = me?.id ?? me?.user?.id ?? null;
if (!myId) throw new Error(`cannot resolve viewer id: ${JSON.stringify(me).slice(0, 300)}`);

const name = `lrm1227-${Date.now().toString(36)}`;
const chRes = await api.post(`${base}/api/channels`, { headers, data: { name } });
if (!chRes.ok()) throw new Error(`POST /api/channels ${chRes.status()} ${await chRes.text()}`);
const channel = await chRes.json();
const chid = channel.id ?? channel.channel?.id;
if (!chid) throw new Error(`no channel id: ${JSON.stringify(channel).slice(0, 400)}`);

// Viewer's own 3-message group — head / middle / tail of one joined shell.
const own = [
  "分组第一条：气泡壳的上边和上圆角画在这一行。",
  "分组第二条：中间行只画左右两条边，所以整组看起来是一个连续的气泡。",
  "分组第三条：这一行画下边和下圆角，把这个气泡收口。",
];
for (const content of own) {
  const res = await api.post(`${base}/api/channels/${chid}/messages`, { headers, data: { content } });
  if (!res.ok()) throw new Error(`POST message ${res.status()} ${await res.text()}`);
}

await page.context().storageState({ path: "/tmp/lrm1227-state.json" });
writeFileSync(
  "/tmp/lrm1227-ctx.json",
  JSON.stringify({ email, workspaceId: ws.id, workspaceSlug: ws.slug, chid, name, myId }, null, 2),
);
await browser.close();
console.log(`seeded #${name} (${chid}) in ${ws.slug}; viewer=${myId}`);
