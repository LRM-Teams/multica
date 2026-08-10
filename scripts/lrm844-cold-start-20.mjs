/**
 * LRM-844 AC2: 20 consecutive desktop cold-start hard refreshes.
 * Prefer production Next with the fix: REMOTE_API_URL=https://api.leagent.me next start
 */
import { chromium } from "playwright";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";

const BASE = process.env.BASE_URL || "http://127.0.0.1:3055";
const AUTH_BASE = process.env.AUTH_BASE || "https://api.leagent.me";
const EMAIL = process.env.QA_EMAIL || "qa-bot@lenovo.com";
const CODE = process.env.QA_CODE || "888888";
const RUNS = Number(process.env.RUNS || 20);
const STUCK_MS = Number(process.env.STUCK_MS || 12000);
const PATH = process.env.APP_PATH || "/lrm-team/channels";
const OUT_DIR = resolve("e2e/artifacts/lrm844-ac2");

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

async function checkNotStuck(page) {
  const deadline = Date.now() + STUCK_MS;
  let sawSkeleton = false;
  let sawSidebar = false;
  while (Date.now() < deadline) {
    const info = await page.evaluate(() => {
      const skel = !!document.querySelector('[data-testid="dm-list-skeleton"]');
      const sidebar =
        !!document.querySelector('[data-testid="list"]') ||
        /私信|消息/.test(document.body?.innerText || "");
      const dmRow =
        document.querySelectorAll('a[href*="dm="]').length > 0 ||
        /私信/.test(document.body?.innerText || "");
      return { skel, sidebar, dmRow };
    });
    if (info.sidebar) sawSidebar = true;
    if (info.skel) sawSkeleton = true;
    if (sawSidebar && !info.skel) {
      return { ok: true, sawSkeleton, stuck: false, sawSidebar };
    }
    await page.waitForTimeout(200);
  }
  const still = await page.evaluate(
    () => !!document.querySelector('[data-testid="dm-list-skeleton"]'),
  );
  return { ok: !still && sawSidebar, sawSkeleton, stuck: still, sawSidebar };
}

async function main() {
  await mkdir(OUT_DIR, { recursive: true });
  const token = await loginToken();
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1280, height: 900 },
  });
  const host = new URL(BASE).hostname;
  await context.addCookies([
    {
      name: "multica_auth",
      value: token,
      domain: host,
      path: "/",
      httpOnly: false,
      secure: BASE.startsWith("https"),
      sameSite: "Lax",
    },
  ]);
  const page = await context.newPage();
  await page.addInitScript((t) => {
    try {
      localStorage.setItem("multica_token", t);
    } catch {
      /* ignore */
    }
  }, token);

  const target = `${BASE}${PATH}`;
  const results = [];

  for (let i = 1; i <= RUNS; i++) {
    const t0 = Date.now();
    // Hard navigation each time (cold-ish); then cache-bust reload
    await page.goto(`${target}?_ac2=${i}&t=${Date.now()}`, {
      waitUntil: "domcontentloaded",
      timeout: 90000,
    });
    const check = await checkNotStuck(page);
    const row = { run: i, ms: Date.now() - t0, ...check, url: page.url() };
    results.push(row);
    console.log(JSON.stringify(row));
    if (check.stuck || !check.sawSidebar) {
      await page.screenshot({
        path: resolve(OUT_DIR, `fail-run-${i}.png`),
        fullPage: false,
      });
    }
  }

  await page.screenshot({
    path: resolve(OUT_DIR, "desktop-after-20.png"),
    fullPage: false,
  });

  const stuck = results.filter((r) => r.stuck).length;
  const noSidebar = results.filter((r) => !r.sawSidebar).length;
  const pass = stuck === 0 && noSidebar === 0;
  const summary = {
    base: BASE,
    target,
    runs: RUNS,
    stuck,
    noSidebar,
    pass,
    results,
  };
  await writeFile(resolve(OUT_DIR, "summary.json"), JSON.stringify(summary, null, 2));
  console.log("---");
  console.log(JSON.stringify({ pass, stuck, noSidebar, runs: RUNS, out: OUT_DIR }));
  await browser.close();
  process.exit(pass ? 0 : 2);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
