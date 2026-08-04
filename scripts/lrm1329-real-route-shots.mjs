/**
 * LRM-1329 real-route gate shots.
 *
 * Seeds DB sessions, logs into the live local app, opens
 * `/{slug}/research/{id}` with the sources aux drawer, and captures:
 * - 20-frame matrix: 1440+390 × light/dark × loading/empty/partial/ready/error
 * - BEFORE (rail, drawer closed) on ready@1440 light
 * - reduced-motion static on ready@1440 light
 * - Escape focus restore to sources trigger
 *
 * Error frames (UI Gate rework): load a running partial session with real
 * chrome + drawer first, then poison GET /api/research/sessions/:id so the
 * next poll keeps cached shell and drives EvidencePulse overview=error
 * (never cold 500 full-page Server error).
 *
 * Env:
 *   LRM1329_ERROR_ONLY=1  — only re-shoot the 4 error frames
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import pg from "pg";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1329-real");
mkdirSync(outDir, { recursive: true });

const API = process.env.NEXT_PUBLIC_API_URL || "http://localhost:18840";
const APP = process.env.PLAYWRIGHT_BASE_URL || "http://localhost:13761";
const DATABASE_URL =
  process.env.DATABASE_URL ||
  "postgres://multica:multica@localhost:5432/multica_multica_760?sslmode=disable";
const ERROR_ONLY = process.env.LRM1329_ERROR_ONLY === "1";

const EMAIL = "e2e-lrm1329-gate@multica.ai";
const NAME = "LRM1329 Gate";
const WS_SLUG = "e2e-lrm1329-ws";
const WS_NAME = "E2E LRM1329 Workspace";

async function db(sql, params = []) {
  const client = new pg.Client(DATABASE_URL);
  await client.connect();
  try {
    return await client.query(sql, params);
  } finally {
    await client.end();
  }
}

async function login() {
  await db("DELETE FROM verification_code WHERE email = $1", [EMAIL]);
  const send = await fetch(`${API}/auth/send-code`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email: EMAIL }),
  });
  if (!send.ok) throw new Error(`send-code ${send.status}`);
  const codeRow = await db(
    "SELECT code FROM verification_code WHERE email = $1 AND used = FALSE AND expires_at > now() ORDER BY created_at DESC LIMIT 1",
    [EMAIL],
  );
  if (!codeRow.rows[0]) throw new Error("no verification code");
  const verify = await fetch(`${API}/auth/verify-code`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email: EMAIL, code: codeRow.rows[0].code }),
  });
  if (!verify.ok) throw new Error(`verify-code ${verify.status}`);
  const data = await verify.json();
  await db("DELETE FROM verification_code WHERE email = $1", [EMAIL]);
  await db('UPDATE "user" SET onboarded_at = now(), name = $2 WHERE email = $1', [
    EMAIL,
    NAME,
  ]);
  return data.token;
}

async function ensureWorkspace(token) {
  const list = await fetch(`${API}/api/workspaces`, {
    headers: { Authorization: `Bearer ${token}` },
  }).then((r) => r.json());
  let ws = list.find((w) => w.slug === WS_SLUG) ?? null;
  if (!ws) {
    const created = await fetch(`${API}/api/workspaces`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ name: WS_NAME, slug: WS_SLUG }),
    });
    if (!created.ok) throw new Error(`create workspace ${created.status}`);
    ws = await created.json();
  }
  return ws;
}

async function seedSessions(workspaceId, userId) {
  const fleet = await db(
    "SELECT id FROM research_fleet WHERE workspace_id = $1 LIMIT 1",
    [workspaceId],
  );
  if (!fleet.rows[0]) {
    await db(
      `INSERT INTO research_fleet (id, workspace_id, created_at, updated_at)
       VALUES (gen_random_uuid(), $1, now(), now())`,
      [workspaceId],
    );
  }
  const fleetId = (
    await db("SELECT id FROM research_fleet WHERE workspace_id = $1 LIMIT 1", [
      workspaceId,
    ])
  ).rows[0].id;

  const ids = {};
  for (const [key, status] of [
    ["empty", "drafting"],
    ["loading", "running"],
    ["partial", "running"],
    ["ready", "completed"],
  ]) {
    const row = await db(
      `INSERT INTO research_session
         (workspace_id, fleet_id, created_by, title, goal, status, current_stage)
       VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
      [
        workspaceId,
        fleetId,
        userId,
        `LRM-1329 ${key}`,
        `Gate seed ${key}`,
        status,
        status === "completed" ? "s4_delivery" : "s2_sources",
      ],
    );
    ids[key] = row.rows[0].id;
  }

  // partial + ready: sources + human_ai_boundary finding
  for (const key of ["partial", "ready"]) {
    await db(
      `INSERT INTO research_source
         (id, workspace_id, session_id, url, title, source_class, credibility_weight,
          stance, relevance, summary, excerpt, payload, created_at, updated_at)
       VALUES
         (gen_random_uuid(), $1, $2, 'https://www.rfc-editor.org/rfc/rfc9110', 'RFC 9110',
          'docs', 0.95, '', 1, 'HTTP semantics', '', '{"why":"官方文档提供可复核的规范定义。"}'::jsonb, now(), now()),
         (gen_random_uuid(), $1, $2, 'https://steamdb.info/', 'SteamDB charts',
          'marketplace', 0.8, '', 1, '定价供给', '', '{"why":"领域供给与定价只能从交易侧数据读出。"}'::jsonb, now(), now())`,
      [workspaceId, ids[key]],
    );
    await db(
      `INSERT INTO research_graph_node
         (id, workspace_id, session_id, node_type, title, summary, status, actor_agent_id, payload, created_at, updated_at)
       VALUES
         (gen_random_uuid(), $1, $2, 'finding', '人机边界',
          $3, 'done', null,
          '{"dimension_family":"human_ai_boundary"}'::jsonb, now(), now())`,
      [
        workspaceId,
        ids[key],
        "AI 上限：可检索公开资料并起草对照表，但不能出具持牌合规结论\n必须有人：最终采纳、对外承诺与合规终审必须由人确认\n人做：锁定验收标准与风险阈值 / AI做：汇总候选证据并标注冲突",
      ],
    );
  }

  // error frames reuse the running partial session (poll keeps shell).
  ids.error = ids.partial;
  return ids;
}

async function dismissOverlays(page) {
  try {
    const skip = page.getByRole("button", { name: /^(Skip|跳过)$/i });
    if (await skip.count()) {
      await skip.first().click({ timeout: 2000, force: true });
      await page.waitForTimeout(250);
    }
  } catch {
    /* none */
  }
  try {
    await page.keyboard.press("Escape");
    await page.waitForTimeout(200);
  } catch {
    /* ignore */
  }
  try {
    const card = page.locator('[data-testid="research-completion-card"]');
    if (await card.count()) {
      const closeBtn = card.locator(
        'button[aria-label*="close" i], button[aria-label*="关闭" i], button[aria-label*="dismiss" i]',
      );
      if (await closeBtn.count()) {
        await closeBtn.first().click({ timeout: 1500, force: true });
        await page.waitForTimeout(250);
      } else {
        await page.keyboard.press("Escape");
        await page.waitForTimeout(200);
      }
    }
  } catch {
    /* ignore */
  }
  try {
    await page.keyboard.press("Escape");
    await page.waitForTimeout(150);
  } catch {
    /* ignore */
  }
}

async function openSourcesDrawer(page) {
  await page.waitForSelector(
    '[data-testid="research-module-sources"], [data-testid="research-module-rail"]',
    { timeout: 60000 },
  );
  for (let attempt = 0; attempt < 6; attempt++) {
    await dismissOverlays(page);
    if (await page.locator('[data-testid="research-evidence-pulse"]').count()) {
      return;
    }
    if (await page.locator('[data-testid="source-strategy-strip"]').count()) {
      return;
    }
    const clicked = await page.evaluate(() => {
      const el = document.querySelector(
        '[data-testid="research-module-sources"]',
      );
      if (!el) return false;
      el.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true }),
      );
      return true;
    });
    if (!clicked) {
      try {
        await page
          .locator('[data-testid="research-module-sources"]')
          .click({ timeout: 3000, force: true });
      } catch {
        /* retry */
      }
    }
    try {
      await page.waitForSelector(
        '[data-testid="research-evidence-pulse"], [data-testid="source-strategy-strip"]',
        { timeout: 8000 },
      );
      return;
    } catch {
      await page.waitForTimeout(400);
    }
  }
  throw new Error("could not open sources module / pulse");
}

async function probePulse(page, sid) {
  return page.evaluate((sessionId) => {
    const pulse = document.querySelector(
      '[data-testid="research-evidence-pulse"]',
    );
    const drawer = document.querySelector(
      '[data-testid="research-aux-drawer-chrome"]',
    );
    return {
      path: location.pathname,
      onRoute: location.pathname.includes(`/research/${sessionId}`),
      mode: pulse?.getAttribute("data-mode") ?? null,
      role: pulse?.getAttribute("role") ?? null,
      verification:
        document.querySelector(
          '[data-testid="research-evidence-pulse-verification"]',
        )?.textContent ?? "",
      status:
        document.querySelector(
          '[data-testid="research-evidence-pulse-status"]',
        )?.textContent ?? "",
      hasExpected: !!document.querySelector(
        '[data-testid="research-evidence-pulse-expected"]',
      ),
      hasStrip: !!document.querySelector(
        '[data-testid="source-strategy-strip"]',
      ),
      hasBoundary: !!document.querySelector(
        '[data-testid="human-boundary-card"]',
      ),
      hasRetry: !!pulse?.querySelector("button"),
      hasDrawer: !!drawer,
      hasServerErrorPage: !!document.querySelector(
        '[data-testid="research-server-error-page"]',
      ),
      hasChrome: !!(
        document.querySelector('[data-testid="research-module-rail"]') ||
        document.querySelector('[data-testid="research-session-page"]') ||
        document.querySelector('[data-testid="research-session-chrome"]')
      ),
    };
  }, sid);
}

/** Keep shell + drawer; fail only the snapshot GET so overview becomes error. */
async function armDrawerScopedSnapshotError(page, sid) {
  await page.unrouteAll({ behavior: "ignoreErrors" }).catch(() => {});
  const pathSuffix = `/api/research/sessions/${sid}`;
  await page.route(`**${pathSuffix}`, async (route) => {
    const url = route.request().url();
    // Exact snapshot only — not /presence, /messages, …
    if (!url.includes(`${pathSuffix}?`) && !url.endsWith(pathSuffix)) {
      await route.continue();
      return;
    }
    if (route.request().method() !== "GET") {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: "evidence_unavailable" }),
    });
  });
}

async function waitForDrawerEvidenceError(page, sid) {
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    const probe = await probePulse(page, sid);
    if (
      probe.mode === "error" &&
      probe.hasChrome &&
      !probe.hasServerErrorPage &&
      !probe.hasStrip &&
      !probe.hasBoundary &&
      probe.role === "alert" &&
      probe.hasRetry
    ) {
      return probe;
    }
    await page.waitForTimeout(400);
  }
  const last = await probePulse(page, sid);
  throw new Error(
    `drawer-scoped error not reached: ${JSON.stringify(last)}`,
  );
}

const token = await login();
const me = await fetch(`${API}/api/me`, {
  headers: { Authorization: `Bearer ${token}` },
}).then((r) => r.json());
const userId = me.id || me.user?.id;
if (!userId) throw new Error(`no user id: ${JSON.stringify(me)}`);
const ws = await ensureWorkspace(token);
const sessionIds = await seedSessions(ws.id, userId);
console.log(
  "seeded",
  sessionIds,
  "slug",
  ws.slug,
  "APP",
  APP,
  "API",
  API,
  "ERROR_ONLY",
  ERROR_ONLY,
);

const browser = await chromium.launch({
  executablePath: process.env.CHROME_BIN || undefined,
});

const themes = ["light", "dark"];
const modes = ERROR_ONLY
  ? ["error"]
  : ["loading", "empty", "partial", "ready", "error"];
const viewports = [
  { name: "1440", width: 1440, height: 900 },
  { name: "390", width: 390, height: 844 },
];

const report = [];

async function gotoSession(page, sid) {
  await page.unrouteAll({ behavior: "ignoreErrors" }).catch(() => {});
  const url = `${APP}/${ws.slug}/research/${sid}`;
  await page.goto(url, { waitUntil: "domcontentloaded", timeout: 90000 });
  try {
    const skip = page.getByRole("button", { name: /跳过|Skip/i });
    if (await skip.count()) await skip.first().click({ timeout: 2000 });
  } catch {
    /* none */
  }
  await page.waitForTimeout(700);
}

for (const vp of viewports) {
  const context = await browser.newContext({
    viewport: { width: vp.width, height: vp.height },
  });
  const page = await context.newPage();
  await page.goto(`${APP}/login`, { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.evaluate((t) => localStorage.setItem("multica_token", t), token);

  for (const theme of themes) {
    await page.evaluate((th) => {
      document.documentElement.classList.remove("light", "dark");
      document.documentElement.classList.add(th);
      localStorage.setItem("theme", th);
    }, theme);

    for (const mode of modes) {
      const sid = sessionIds[mode];
      await gotoSession(page, sid);
      await openSourcesDrawer(page);

      let probe;
      if (mode === "error") {
        // Running partial session polls every 4s — poison after drawer is open.
        await armDrawerScopedSnapshotError(page, sid);
        probe = await waitForDrawerEvidenceError(page, sid);
        if (
          !/暂时无法读取依据|Could not read evidence|unable to read evidence/i.test(
            probe.status,
          )
        ) {
          throw new Error(`unsafe/missing error status: ${probe.status}`);
        }
      } else {
        await page.waitForTimeout(350);
        probe = await probePulse(page, sid);
        if (!probe.onRoute && !probe.hasChrome) {
          throw new Error(`not on real route: ${probe.path}`);
        }
        if (!probe.mode) {
          throw new Error(`missing pulse mode @ ${vp.name}/${theme}/${mode}`);
        }
        if (mode === "loading" && probe.mode !== "loading") {
          console.warn("warn loading mode got", probe.mode);
        }
        if (mode === "ready" && probe.mode !== "ready") {
          console.warn("warn ready mode got", probe.mode);
        }
        if (mode === "partial" && probe.mode !== "partial") {
          console.warn("warn partial mode got", probe.mode);
        }
        if (mode === "empty" && probe.mode !== "empty") {
          console.warn("warn empty mode got", probe.mode);
        }
      }

      const file = `${vp.name}--${theme}--${mode}.png`;
      await page.screenshot({ path: resolve(outDir, file), fullPage: false });
      report.push({ file, ...probe, opened: true });
      console.log("shot", file, probe.mode, probe.verification.slice(0, 24));
    }
  }
  await context.close();
}

if (!ERROR_ONLY) {
  // Extra evidence: BEFORE / reduced-motion / Escape focus (1440 light ready)
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();
  await page.goto(`${APP}/login`, { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.evaluate((t) => localStorage.setItem("multica_token", t), token);
  await page.evaluate(() => {
    document.documentElement.classList.remove("dark");
    document.documentElement.classList.add("light");
    localStorage.setItem("theme", "light");
  });

  const sid = sessionIds.ready;
  await gotoSession(page, sid);
  await dismissOverlays(page);
  await page.waitForSelector('[data-testid="research-module-sources"]', {
    timeout: 60000,
  });
  await page.screenshot({
    path: resolve(outDir, "BEFORE--1440--light--ready-rail.png"),
    fullPage: false,
  });
  report.push({ file: "BEFORE--1440--light--ready-rail.png", kind: "before" });
  console.log("shot BEFORE--1440--light--ready-rail.png");

  await page.emulateMedia({ reducedMotion: "reduce" });
  await openSourcesDrawer(page);
  await page.waitForTimeout(500);
  await page.screenshot({
    path: resolve(outDir, "reduced-motion--1440--light--ready.png"),
    fullPage: false,
  });
  report.push({
    file: "reduced-motion--1440--light--ready.png",
    kind: "reduced-motion",
  });
  console.log("shot reduced-motion--1440--light--ready.png");

  await page.emulateMedia({ reducedMotion: "no-preference" });
  await dismissOverlays(page);
  try {
    await page.keyboard.press("Escape");
    await page.waitForTimeout(200);
  } catch {
    /* ignore */
  }
  const trigger = page.locator('[data-testid="research-module-sources"]');
  await trigger.focus();
  await trigger.click();
  await page.waitForSelector('[data-testid="research-evidence-pulse"]', {
    timeout: 15000,
  });
  await page.keyboard.press("Escape");
  await page.waitForTimeout(400);
  const focusInfo = await page.evaluate(() => {
    const active = document.activeElement;
    return {
      testId: active?.getAttribute?.("data-testid") ?? null,
      tag: active?.tagName ?? null,
      ariaLabel: active?.getAttribute?.("aria-label") ?? null,
      text: (active?.textContent || "").trim().slice(0, 40),
    };
  });
  await page.screenshot({
    path: resolve(outDir, "escape-focus--1440--light--ready.png"),
    fullPage: false,
  });
  report.push({
    file: "escape-focus--1440--light--ready.png",
    kind: "escape-focus",
    focusInfo,
  });
  console.log("shot escape-focus--1440--light--ready.png", focusInfo);
  await context.close();
}

await browser.close();
const summary = { ok: true, count: report.length, outDir, report };
writeFileSync(resolve(outDir, "report.json"), JSON.stringify(summary, null, 2));
console.log(JSON.stringify(summary, null, 2));
