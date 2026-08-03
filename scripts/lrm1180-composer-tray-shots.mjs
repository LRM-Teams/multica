#!/usr/bin/env node
/**
 * LRM-1180 evidence — real dev stack, real channel composer, real file upload.
 * Usage: node scripts/lrm1180-composer-tray-shots.mjs <before|after>
 *
 * Captures, at desktop (1280) and mobile-web (390, coarse pointer):
 *   1. the tray as it looks with one image + one file pending
 *   2. hover / focus state of the image thumb
 *   3. the measured geometry the frozen v2 design is specified in:
 *      remove-button size, occluded thumb area %, pointer hit box, and whether
 *      the button survives the tray's own `overflow-y-hidden` clip
 *   4. (after only) the zoom path: click thumb → shared preview modal
 */
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const label = process.argv[2] ?? "shot";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1180");
mkdirSync(outDir, { recursive: true });

const base = process.env.SHOT_BASE_URL ?? "http://localhost:13860";
const ctx = JSON.parse(readFileSync("/tmp/lrm1180-ctx.json", "utf8"));
const IMAGE = process.env.SHOT_IMAGE ?? "/tmp/lrm1180/states.png";
const FILE = "/tmp/lrm1180-notes.txt";
writeFileSync(FILE, "requirements for the frozen v2 tray\n");

const report = [];
const log = (line) => {
  console.log(`[${label}] ${line}`);
  report.push(line);
};

const browser = await chromium.launch();

/** Geometry the design is specified in — measured, not asserted. */
const measure = () =>
  ({
    tray: (() => {
      const ul = document.querySelector('[data-testid="composer-attachment-tray"]');
      if (!ul) return null;
      const cs = getComputedStyle(ul);
      const r = ul.getBoundingClientRect();
      return {
        gap: cs.columnGap,
        paddingTop: cs.paddingTop,
        paddingRight: cs.paddingRight,
        marginTop: cs.marginTop,
        overflowY: cs.overflowY,
        // The clip region of an `overflow` box is its *padding* box.
        clipTop: r.top + parseFloat(cs.borderTopWidth || "0"),
        clipRight: r.right - parseFloat(cs.borderRightWidth || "0"),
      };
    })(),
    items: [...document.querySelectorAll('[data-testid^="composer-tray-item-"]')].map(
      (li) => {
        const box = (el) => {
          if (!el) return null;
          const r = el.getBoundingClientRect();
          return { x: r.x, y: r.y, w: r.width, h: r.height, top: r.top, right: r.right };
        };
        const img = li.querySelector("img");
        const zoom = li.querySelector('[data-testid^="composer-tray-zoom-"]');
        const remove = [...li.querySelectorAll("button")].find((b) =>
          /移除|Remove/.test(b.getAttribute("aria-label") ?? ""),
        );
        let after = null;
        if (remove) {
          const cs = getComputedStyle(remove, "::after");
          after = {
            top: cs.top,
            right: cs.right,
            bottom: cs.bottom,
            left: cs.left,
            content: cs.content,
            position: cs.position,
          };
        }
        return {
          id: li.getAttribute("data-testid"),
          kind: li.getAttribute("data-kind"),
          status: li.getAttribute("data-status"),
          thumb: box(img),
          zoomButton: box(zoom),
          zoomAria: zoom?.getAttribute("aria-label") ?? null,
          remove: box(remove),
          removeAfter: after,
          removeVisible: remove ? getComputedStyle(remove).opacity : null,
        };
      },
    ),
  });

function analyse(m, viewport) {
  log(`--- ${viewport} tray: gap=${m.tray.gap} padTop=${m.tray.paddingTop} padRight=${m.tray.paddingRight} marginTop=${m.tray.marginTop} overflowY=${m.tray.overflowY}`);
  for (const it of m.items) {
    if (it.kind !== "image" || !it.thumb || !it.remove) {
      log(`${viewport} ${it.kind} chip: remove=${it.remove ? `${Math.round(it.remove.w)}px` : "none"} (unchanged path)`);
      continue;
    }
    const t = it.thumb;
    const r = it.remove;
    const ox = Math.max(0, Math.min(t.x + t.w, r.x + r.w) - Math.max(t.x, r.x));
    const oy = Math.max(0, Math.min(t.y + t.h, r.y + r.h) - Math.max(t.y, r.y));
    const occlusion = ((ox * oy) / (t.w * t.h)) * 100;
    const pad = (v) => (v && v !== "auto" ? Math.abs(parseFloat(v)) : 0);
    const a = it.removeAfter;
    const hit = a && a.position === "absolute"
      ? { w: r.w + pad(a.left) + pad(a.right), h: r.h + pad(a.top) + pad(a.bottom) }
      : { w: r.w, h: r.h };
    const clippedTop = r.top < m.tray.clipTop - 0.5;
    const clippedRight = r.right > m.tray.clipRight + 0.5;
    log(
      `${viewport} image thumb ${Math.round(t.w)}px · remove ${Math.round(r.w)}px @${r.x < t.x + t.w ? "" : "outdented "}` +
        `occlusion ${occlusion.toFixed(1)}% · hit ${Math.round(hit.w)}×${Math.round(hit.h)}px (SC 2.5.8 ${hit.w >= 24 && hit.h >= 24 ? "PASS" : "FAIL"})`,
    );
    log(
      `${viewport} clip check: remove.top=${r.top.toFixed(1)} trayClipTop=${m.tray.clipTop.toFixed(1)} → ${clippedTop ? "CLIPPED" : "not clipped"}; ` +
        `remove.right=${r.right.toFixed(1)} trayClipRight=${m.tray.clipRight.toFixed(1)} → ${clippedRight ? "CLIPPED" : "not clipped"}`,
    );
    log(`${viewport} zoom entry point: ${it.zoomButton ? `button ${Math.round(it.zoomButton.w)}px aria="${it.zoomAria}"` : "NONE (image not clickable)"}`);
  }
}

async function run({ viewport, name, isMobile }) {
  const page = await browser.newPage({
    viewport,
    deviceScaleFactor: 2,
    isMobile,
    hasTouch: isMobile,
    locale: "zh-CN",
  });
  page.setDefaultTimeout(90_000);
  page.setDefaultNavigationTimeout(120_000);

  await page.goto(`${base}/login`, { waitUntil: "domcontentloaded" });
  await page.evaluate((t) => localStorage.setItem("multica_token", t), ctx.token);

  // The web build shows a "download the desktop app / continue on web" gate
  // before any authenticated route renders.
  const passGate = async () => {
    for (const name of [/在 ?web ?端继续/i, /continue (on|in) (the )?web/i, /^跳过$/, /^Skip$/i]) {
      const btn = page.getByRole("button", { name }).first();
      if (await btn.isVisible().catch(() => false)) {
        await btn.click().catch(() => {});
        await page.waitForTimeout(1500);
        continue;
      }
      const link = page.getByRole("link", { name }).first();
      if (await link.isVisible().catch(() => false)) {
        await link.click().catch(() => {});
        await page.waitForTimeout(1500);
      }
    }
  };

  const url = `${base}/${ctx.slug}/channels/${ctx.channelId}`;
  for (let attempt = 0; attempt < 4; attempt += 1) {
    await page.goto(url, { waitUntil: "domcontentloaded" });
    await passGate();
    await page.waitForLoadState("networkidle", { timeout: 60_000 }).catch(() => {});
    await passGate();
    if (await page.locator('input[type="file"]').first().count()) break;
    console.log(`[${label}] retry channel route (attempt ${attempt + 1})`);
    await page.waitForTimeout(2000);
  }
  await page.waitForTimeout(1500);

  const input = page.locator('input[type="file"]').first();
  await input.setInputFiles([IMAGE, FILE]);
  await page.getByTestId("composer-attachment-tray").waitFor({ timeout: 60_000 });
  // Let the upload settle so the shot is the steady `ready` state.
  await page.waitForTimeout(4000);

  await page.screenshot({ path: `${outDir}/${label}-${name}-tray.png` });

  // Desktop chrome is hover-gated; mobile has no hover so the resting shot is
  // already the "controls visible" state.
  const thumbLi = page.locator('[data-testid^="composer-tray-item-"][data-kind="image"]').first();
  await thumbLi.hover().catch(() => {});
  await page.waitForTimeout(600);
  await page.screenshot({ path: `${outDir}/${label}-${name}-tray-hover.png` });
  analyse(await page.evaluate(measure), name);

  // Zoom path (only exists after the change).
  const zoom = page.locator('[data-testid^="composer-tray-zoom-"]').first();
  if (await zoom.count()) {
    await zoom.click();
    await page.waitForTimeout(2500);
    await page.screenshot({ path: `${outDir}/${label}-${name}-preview.png` });
    const dialog = await page.locator('[role="dialog"]').count();
    const previewImg = await page
      .locator('[role="dialog"] img')
      .first()
      .getAttribute("src")
      .catch(() => null);
    log(`${name} preview modal: dialogs=${dialog} img=${previewImg ? previewImg.slice(0, 32) : "none"}`);
    await page.keyboard.press("Escape");
    await page.waitForTimeout(1200);
    const focused = await page.evaluate(() =>
      document.activeElement?.getAttribute("data-testid") ?? document.activeElement?.tagName,
    );
    log(`${name} focus after close → ${focused}`);
    await page.screenshot({ path: `${outDir}/${label}-${name}-after-close.png` });
  } else {
    log(`${name} preview modal: no zoom entry point to click`);
  }

  await page.close();
}

await run({ viewport: { width: 1280, height: 860 }, name: "desktop", isMobile: false });
await run({ viewport: { width: 390, height: 820 }, name: "mobile", isMobile: true });

await browser.close();
writeFileSync(`${outDir}/${label}-report.txt`, report.join("\n") + "\n");
console.log(`\nreport → ${outDir}/${label}-report.txt`);
