#!/usr/bin/env node
/**
 * LRM-1077 Gate Shots — fullscreen shell states (desktop + narrow).
 * Mirrors the implemented VoiceCallPanel shell (not the old Dialog card).
 */
import { mkdirSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outDir = resolve(root, "e2e/artifacts/lrm1077");
mkdirSync(outDir, { recursive: true });

const html = `<!DOCTYPE html>
<html lang="zh-CN"><head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<style>
  :root{--bg:#0b0b0c;--fg:#fff;--muted:rgba(255,255,255,.6);--red:#ef4444;--green:#22c55e;--chip:rgba(255,255,255,.12)}
  *{box-sizing:border-box} body{margin:0;font:14px/1.4 system-ui,sans-serif;background:#111;color:var(--fg)}
  .shell{position:relative;width:100vw;height:100vh;background:radial-gradient(120% 80% at 50% 0%,#2a2a2e 0%,#0b0b0c 55%);display:flex;flex-direction:column}
  .mini{position:absolute;left:12px;top:12px;width:36px;height:36px;border-radius:8px;background:var(--chip);display:grid;place-items:center}
  .center{flex:1;display:flex;flex-direction:column;align-items:center;justify-content:center;text-align:center;padding:0 24px}
  .av{width:88px;height:88px;border-radius:18px;background:#1f2937;display:grid;place-items:center;font-size:34px;font-weight:700;margin-bottom:14px}
  .name{font-size:22px;font-weight:650}.status{margin-top:8px;color:var(--muted)}.dur{margin-top:6px;color:var(--muted);font-variant-numeric:tabular-nums}
  .actions{display:flex;justify-content:center;gap:36px;padding:0 24px 40px}
  .btn{width:72px;text-align:center}.btn .c{width:64px;height:64px;margin:0 auto 8px;border-radius:999px;display:grid;place-items:center;font-size:22px}
  .red{background:var(--red)}.green{background:var(--green)}.chip{background:var(--chip)}.chip.on{background:#fff;color:#111}
  .btn span{font-size:12px;color:#e5e7eb}
  .pip{position:fixed;right:16px;bottom:24px;display:flex;align-items:center;gap:12px;background:rgba(11,11,12,.95);border:1px solid rgba(255,255,255,.1);border-radius:16px;padding:10px 12px;max-width:240px}
  .pip .av{width:40px;height:40px;font-size:16px;margin:0;border-radius:10px}
</style></head><body>
<div id="root"></div>
<script>
const states = {
  incoming: () => shell('邀请你语音通话…', null, [
    btn('red','拒绝'), btn('green','接听')
  ]),
  incall_speaker_on: () => shell('通话中', '00:42', [
    btn('chip','静音'), btn('chip on','免提'), btn('red','挂断')
  ]),
  incall_speaker_off: () => shell('通话中', '00:42', [
    btn('chip','静音'), btn('chip','免提'), btn('red','挂断')
  ]),
  pip: () => '<div class="shell" style="opacity:.35"></div><div class="pip"><div class="av">A</div><div><div class="name" style="font-size:14px">Agent</div><div class="status" style="font-size:12px">00:42</div></div></div>',
};
function btn(cls, label){return '<div class="btn"><div class="c '+cls+'">●</div><span>'+label+'</span></div>'}
function shell(status, dur, actions){
  return '<div class="shell" data-testid="voice-call-fullscreen"><div class="mini">⧉</div><div class="center"><div class="av">A</div><div class="name">Agent</div><div class="status">'+status+'</div>'+(dur?'<div class="dur">'+dur+'</div>':'')+'</div><div class="actions">'+actions.join('')+'</div></div>';
}
window.renderState = (name) => { document.getElementById('root').innerHTML = states[name](); };
</script></body></html>`;

const shots = [
  ["incoming", "gate-A-incoming"],
  ["incall_speaker_on", "gate-B-speaker-on"],
  ["incall_speaker_off", "gate-C-speaker-off"],
  ["pip", "gate-D-pip"],
];

const viewports = [
  { name: "narrow", width: 390, height: 844 },
  { name: "desktop", width: 1280, height: 800 },
];

const browser = await chromium.launch();
const page = await browser.newPage();
await page.setContent(html, { waitUntil: "domcontentloaded" });

for (const vp of viewports) {
  await page.setViewportSize({ width: vp.width, height: vp.height });
  for (const [state, base] of shots) {
    await page.evaluate((s) => window.renderState(s), state);
    await page.waitForTimeout(80);
    const path = resolve(outDir, `${base}-${vp.name}.png`);
    await page.screenshot({ path, fullPage: true });
    console.log("wrote", path);
  }
}
await browser.close();
console.log("done", outDir);
