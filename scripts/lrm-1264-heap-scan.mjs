/**
 * LRM-1264 — JS heap soft-budget probe (desktop + narrow).
 *
 * Usage:
 *   BASE_URL=http://127.0.0.1:13797 TOKEN=... node scripts/lrm-1264-heap-scan.mjs
 *   # or QA login against AUTH_BASE (default https://api.leagent.me)
 *
 * Ready signal: channel title button "Open channel details" (same as LRM-1182 R2).
 */
import { chromium } from "playwright";

const BASE = process.env.BASE_URL || "http://127.0.0.1:13797";
const AUTH_BASE = process.env.AUTH_BASE || "https://api.leagent.me";
const EMAIL = process.env.QA_EMAIL || "qa-bot@lenovo.com";
const CODE = process.env.QA_CODE || "888888";
const PATH = process.env.APP_PATH || "/lrm-team/channels";
const READY_MS = Number(process.env.READY_MS || 90000);

async function loginToken() {
  if (process.env.TOKEN) return process.env.TOKEN;
  const send = await fetch(`${AUTH_BASE}/auth/send-code`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ email: EMAIL }),
  });
  if (!send.ok) throw new Error(`send-code ${send.status}`);
  const verify = await fetch(`${AUTH_BASE}/auth/verify-code`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ email: EMAIL, code: CODE }),
  });
  if (!verify.ok) throw new Error(`verify-code ${verify.status}`);
  const body = await verify.json();
  if (!body.token) throw new Error("no token");
  return body.token;
}

async function safeEval(page, fn) {
  for (let i = 0; i < 8; i++) {
    try {
      return await page.evaluate(fn);
    } catch (err) {
      const msg = String(err?.message || err);
      if (!/Execution context was destroyed|navigation/i.test(msg)) throw err;
      await page.waitForLoadState("domcontentloaded", { timeout: 15000 }).catch(() => {});
      await page.waitForTimeout(300);
    }
  }
  return null;
}

async function waitReady(page) {
  const deadline = Date.now() + READY_MS;
  // Prefer an already-open channel; otherwise open the first sidebar channel.
  while (Date.now() < deadline) {
    const ok = await safeEval(
      page,
      () => !!document.querySelector('button[aria-label="Open channel details"]'),
    );
    if (ok) return true;
    const clicked = await safeEval(page, () => {
      const links = Array.from(
        document.querySelectorAll('a[href*="/channels/"]'),
      ).filter((a) => /\/channels\/[^/]+/.test(a.getAttribute("href") || ""));
      if (links[0]) {
        links[0].click();
        return true;
      }
      return false;
    });
    if (clicked) {
      await page.waitForTimeout(500);
      continue;
    }
    await page.waitForTimeout(250);
  }
  return false;
}

async function measure(page, viewport) {
  await page.setViewportSize(viewport);
  await page.goto(`${BASE}${PATH}`, { waitUntil: "networkidle", timeout: READY_MS }).catch(async () => {
    await page.goto(`${BASE}${PATH}`, { waitUntil: "domcontentloaded", timeout: READY_MS });
  });
  // Close onboarding modal if present (LRM-1182 path).
  await safeEval(page, async () => {
    try {
      const token = localStorage.getItem("multica_token");
      if (!token) return;
      await fetch("/api/me/onboarding", {
        method: "PATCH",
        headers: {
          "content-type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ hdyhau_completed: true }),
      });
    } catch {
      /* ignore */
    }
  });
  const ready = await waitReady(page);
  // Switch a few sidebar channels if present to exercise eviction on after builds.
  const switched = await safeEval(page, async () => {
    const links = Array.from(
      document.querySelectorAll('a[href*="/channels/"]'),
    ).slice(0, 4);
    let n = 0;
    for (const a of links) {
      a.click();
      n += 1;
      await new Promise((r) => setTimeout(r, 400));
    }
    return n;
  });
  await page.waitForTimeout(1500);
  const heap = await safeEval(page, () => {
    const m = performance.memory;
    if (!m) return null;
    return {
      usedMB: Math.round((m.usedJSHeapSize / 1024 / 1024) * 10) / 10,
      totalMB: Math.round((m.totalJSHeapSize / 1024 / 1024) * 10) / 10,
      limitMB: Math.round((m.jsHeapSizeLimit / 1024 / 1024) * 10) / 10,
    };
  });
  const url = page.url();
  return { ready, switched: switched ?? 0, heap, viewport, url };
}

async function main() {
  const token = await loginToken();
  const browser = await chromium.launch({
    headless: true,
    args: ["--js-flags=--expose-gc", "--enable-precise-memory-info"],
  });
  const context = await browser.newContext();
  const host = new URL(BASE).hostname;
  const cookieDomain = host.startsWith("127.") || host === "localhost" ? host : `.${host.replace(/^\./, "")}`;
  await context.addCookies([
    {
      name: "multica_auth",
      value: token,
      domain: cookieDomain,
      path: "/",
      httpOnly: false,
      secure: BASE.startsWith("https"),
    },
  ]);
  await context.addInitScript((t) => {
    localStorage.setItem("multica_token", t);
  }, token);

  const page = await context.newPage();
  const desktop = await measure(page, { width: 1440, height: 900 });
  const narrow = await measure(page, { width: 390, height: 844 });
  await browser.close();

  const report = {
    base: BASE,
    path: PATH,
    desktop,
    narrow,
    budgets: { desktopMB: 150, narrowMB: 100 },
    baseline_lrm1182_next_dev_MB: 440.6,
  };
  console.log(JSON.stringify(report, null, 2));
  if (!desktop.ready || !narrow.ready) process.exitCode = 2;
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
