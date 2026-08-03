#!/usr/bin/env node
/**
 * LRM-1185 fixture — real workspace + channel + one human message + one agent,
 * so the narrow-screen profile surfaces (member page AND agent page) can be
 * reached the way Frank reaches them: tap the avatar in a channel message.
 *
 * Writes /tmp/lrm1185-ctx.json for the screenshot script.
 */
import { writeFileSync } from "node:fs";
import pg from "pg";

const API = process.env.API_BASE ?? "http://localhost:18396";
const DB = process.env.DATABASE_URL;
const EMAIL = "frank@lrm1185.test";

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
  const res = await fetch(`${API}${path}`, {
    ...init,
    headers: { ...headers, ...(init.headers ?? {}) },
  });
  if (!res.ok) throw new Error(`${path} → ${res.status} ${await res.text()}`);
  return res.json();
}

let workspaces = await api("/api/workspaces");
let ws = workspaces.find((w) => w.slug === "lrm1185");
if (!ws) {
  ws = await api("/api/workspaces", {
    method: "POST",
    body: JSON.stringify({ name: "LRM-1185", slug: "lrm1185" }),
  });
}
const wsHeaders = { "X-Workspace-Id": ws.id };

const list = await api("/api/channels", { headers: wsHeaders });
const channels = Array.isArray(list) ? list : (list.channels ?? []);
const channel =
  channels.find((c) => c.name === "pr-frontend") ??
  (await api("/api/channels", {
    method: "POST",
    headers: wsHeaders,
    body: JSON.stringify({ name: "pr-frontend" }),
  }));

const agents = await api("/api/agents", { headers: wsHeaders });
const agentList = Array.isArray(agents) ? agents : (agents.agents ?? []);
// Agent creation needs a bound runtime, which is daemon plumbing this fixture
// does not care about — insert a runtime + agent row directly so the real
// server/route resolves a real agent for the profile page.
async function ensureAgent() {
  const client = new pg.Client(DB);
  await client.connect();
  try {
    const existing = await client.query(
      "SELECT id FROM agent WHERE workspace_id = $1 AND name = $2",
      [ws.id, "lrm1185-fe"],
    );
    if (existing.rows[0]) return existing.rows[0].id;
    const runtime = await client.query(
      "INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider) VALUES ($1, $2, 'local', 'kiro') RETURNING id",
      [ws.id, "lrm1185-runtime"],
    );
    const inserted = await client.query(
      `INSERT INTO agent (workspace_id, name, display_name, description, avatar_url, runtime_mode, runtime_id)
       VALUES ($1, $2, $3, $4, '/agent-avatars/human-20.jpg', 'local', $5) RETURNING id`,
      [
        ws.id,
        "lrm1185-fe",
        "窄屏资料页 Agent",
        "窄屏详情卡关闭路径回归用",
        runtime.rows[0].id,
      ],
    );
    return inserted.rows[0].id;
  } finally {
    await client.end();
  }
}

const agentId = await ensureAgent();
const agent = { id: agentId };

await api(`/api/channels/${channel.id}/messages`, {
  method: "POST",
  headers: wsHeaders,
  body: JSON.stringify({ content: "窄屏点我的头像应该能退出资料页" }),
});

const ctx = {
  token,
  slug: ws.slug,
  workspaceId: ws.id,
  channelId: channel.id,
  agentId: agent.id,
  userId: auth.user?.id ?? auth.user_id,
};
writeFileSync("/tmp/lrm1185-ctx.json", JSON.stringify(ctx, null, 2));
console.log(ctx);
