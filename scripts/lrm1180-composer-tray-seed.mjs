#!/usr/bin/env node
/**
 * LRM-1180 fixture — a real workspace + channel to open the composer in.
 * The tray items themselves come from a real drag/drop-equivalent file input
 * in the shot script (that is the user path under test), so this only needs to
 * get us logged in and standing in front of a channel composer.
 *
 * Writes /tmp/lrm1180-ctx.json for the screenshot script.
 */
import { writeFileSync } from "node:fs";
import pg from "pg";

const API = process.env.API_BASE ?? "http://localhost:18940";
const DB = process.env.DATABASE_URL;
const EMAIL = "frank@lrm1180.test";

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
    if (!send.ok) throw new Error(`send-code ${send.status} ${await send.text()}`);
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
    const auth = await verify.json();
    // `onboarded_at` is the hard gate on /<slug>/* (apps/web/app/[workspaceSlug]/layout.tsx),
    // and the first-run source questionnaire otherwise covers the whole page.
    await client.query(
      `UPDATE "user" SET onboarded_at = COALESCE(onboarded_at, now()),
              onboarding_questionnaire = jsonb_build_object(
                'source', jsonb_build_array('at_work'), 'source_skipped', true)
       WHERE email = $1`,
      [EMAIL],
    );
    return auth;
  } finally {
    await client.end();
  }
}

const auth = await login();
const token = auth.token;
const headers = {
  "Content-Type": "application/json",
  Authorization: `Bearer ${token}`,
};

async function api(path, init = {}) {
  const res = await fetch(`${API}${path}`, {
    ...init,
    headers: { ...headers, ...(init.headers ?? {}) },
  });
  if (!res.ok) throw new Error(`${path} → ${res.status} ${await res.text()}`);
  return res.json();
}

let workspaces = await api("/api/workspaces");
let ws = workspaces.find((w) => w.slug === "lrm1180");
if (!ws) {
  ws = await api("/api/workspaces", {
    method: "POST",
    body: JSON.stringify({ name: "LRM-1180", slug: "lrm1180" }),
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

const ctx = {
  token,
  slug: ws.slug,
  workspaceId: ws.id,
  channelId: channel.id,
  userId: auth.user?.id ?? auth.user_id,
};
writeFileSync("/tmp/lrm1180-ctx.json", JSON.stringify(ctx, null, 2));
console.log(ctx);
