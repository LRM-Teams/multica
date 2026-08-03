#!/usr/bin/env node
/**
 * LRM-1153 fixture — seeds a real workspace + two channels and posts a patrol
 * message that writes a bare `#channel-name` in prose (exactly the shape from
 * Frank's report), through the real send-message API so the server-side
 * enrichment pipeline is the thing under test.
 *
 * Writes /tmp/lrm1153-ctx.json for the screenshot script.
 */
import { writeFileSync } from "node:fs";
import pg from "pg";

const API = process.env.API_BASE ?? "http://localhost:18736";
const DB = process.env.DATABASE_URL;
const EMAIL = "frank@lrm1153.test";

async function login() {
  const client = new pg.Client(DB);
  await client.connect();
  try {
    await client.query("DELETE FROM verification_code WHERE email = $1", [EMAIL]);
    const send = await fetch(`${API}/auth/send-code`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: EMAIL }),
    });
    if (!send.ok) throw new Error(`send-code ${send.status}`);
    const { rows } = await client.query(
      "SELECT code FROM verification_code WHERE email = $1 AND used = FALSE ORDER BY created_at DESC LIMIT 1",
      [EMAIL],
    );
    const verify = await fetch(`${API}/auth/verify-code`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: EMAIL, code: rows[0].code }),
    });
    if (!verify.ok) throw new Error(`verify-code ${verify.status}`);
    return verify.json();
  } finally {
    await client.end();
  }
}

const auth = await login();
const token = auth.token;
const headers = { "Content-Type": "application/json", Authorization: `Bearer ${token}` };

async function api(path, init = {}) {
  const res = await fetch(`${API}${path}`, { ...init, headers: { ...headers, ...(init.headers ?? {}) } });
  if (!res.ok) throw new Error(`${path} → ${res.status} ${await res.text()}`);
  return res.json();
}

let workspaces = await api("/api/workspaces");
let ws = workspaces.find((w) => w.slug === "lrm1153");
if (!ws) {
  ws = await api("/api/workspaces", {
    method: "POST",
    body: JSON.stringify({ name: "LRM-1153", slug: "lrm1153" }),
  });
}
const wsHeaders = { "X-Workspace-Id": ws.id };

async function ensureChannel(name) {
  const list = await api("/api/channels", { headers: wsHeaders });
  const found = (Array.isArray(list) ? list : (list.channels ?? [])).find((c) => c.name === name);
  if (found) return found;
  return api("/api/channels", {
    method: "POST",
    headers: wsHeaders,
    body: JSON.stringify({ name }),
  });
}

const target = await ensureChannel("pr-frontend");
const patrol = await ensureChannel("patrol-inspection");

// The reported shape: a bare `#name` next to a mention and an issue key, all in
// one message, so the screenshot shows whether the three render consistently.
const issue = await api("/api/issues", {
  method: "POST",
  headers: wsHeaders,
  body: JSON.stringify({
    title: "Thread 预览不显示「N 条新」",
    status: "todo",
    priority: "medium",
    allow_duplicate: true,
  }),
});

const content = `巡检增量 #${target.name} 新反馈 → ${issue.identifier} 待核；设计门现刀 @${auth.user.name}：冻前不拆 FE。`;
const message = await api(`/api/channels/${patrol.id}/messages`, {
  method: "POST",
  headers: wsHeaders,
  body: JSON.stringify({ content }),
});

const refs = (message.parts ?? []).filter((p) => p.type === "reference");
console.log(`content: ${content}`);
console.log(`reference parts: ${JSON.stringify(refs, null, 1)}`);

writeFileSync(
  "/tmp/lrm1153-ctx.json",
  JSON.stringify(
    {
      slug: ws.slug,
      token,
      wsid: ws.id,
      patrol: patrol.id,
      target: target.id,
      message: message.id,
      content,
      refTypes: refs.map((r) => r.ref_type),
    },
    null,
    1,
  ),
);
console.log(`ctx → /tmp/lrm1153-ctx.json (channel ${patrol.id})`);
