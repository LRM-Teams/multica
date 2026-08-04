// LRM-1266 — real-Chromium focus probe.
//
// JSDOM does NOT emulate the browser's "a focused element that becomes natively
// disabled loses focus" rule, so the regression test can only assert the DOM
// shape (native `disabled` present / focusable-node count). This probe takes the
// REAL idle-frame markup emitted by `VoiceMessageAudio` (dumped from the
// component itself), focuses the play control the way a keyboard user does, then
// applies the exact attribute delta React applies on `setState("loading")` and
// measures `document.activeElement` in Chromium.
//
// Usage: node scripts/lrm1266-voice-focus-chromium-probe.mjs <before.html> <after.html>
import { chromium } from "playwright";
import { readFileSync } from "node:fs";

const [beforePath, afterPath] = process.argv.slice(2);
if (!beforePath || !afterPath) {
  console.error("usage: node scripts/lrm1266-voice-focus-chromium-probe.mjs <before.html> <after.html>");
  process.exit(2);
}

const FOCUSABLE =
  'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';

async function measure(page, html, variant) {
  await page.setContent(`<!doctype html><html><body>${html}</body></html>`);
  return page.evaluate(
    ([variantName, focusableSelector]) => {
      const bubble = document.querySelector('[data-testid="voice-reply"]');
      const control = document.querySelector('[data-testid="voice-reply-control"]');
      control.focus();
      const focusedBefore =
        document.activeElement === control ? "voice-reply-control" : document.activeElement.tagName;

      // The loading frame, exactly as each variant renders it.
      if (variantName === "before") {
        control.setAttribute("disabled", "");
        control.setAttribute("aria-busy", "true");
      } else {
        control.setAttribute("aria-disabled", "true");
        control.setAttribute("aria-busy", "true");
        const live = bubble.querySelector('[aria-live="polite"].sr-only');
        if (live) live.textContent = "Preparing voice reply…";
      }

      const active = document.activeElement;
      return {
        variant: variantName,
        focusedBeforeStateFlip: focusedBefore,
        activeElementAfterStateFlip:
          active === control
            ? "voice-reply-control"
            : active === document.body
              ? "BODY"
              : active.tagName,
        focusableNodesInBubble: bubble.querySelectorAll(focusableSelector).length,
        controlNativeDisabled: control.hasAttribute("disabled"),
        controlAriaDisabled: control.getAttribute("aria-disabled"),
        controlAriaLive: control.getAttribute("aria-live"),
        standingLiveRegionText:
          bubble.querySelector('[aria-live="polite"].sr-only')?.textContent ?? null,
      };
    },
    [variant, FOCUSABLE],
  );
}

const browser = await chromium.launch();
const page = await browser.newPage();
const before = await measure(page, readFileSync(beforePath, "utf8"), "before");
const after = await measure(page, readFileSync(afterPath, "utf8"), "after");
await browser.close();

console.log(JSON.stringify({ before, after }, null, 2));
