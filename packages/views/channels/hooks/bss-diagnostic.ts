/**
 * DIAGNOSTIC ONLY — bottom-settle (around-seq false-complete) trace recorder.
 *
 * Temporary instrumentation to capture the internal causal chain of the
 * read-channel (around-seq) "opens at top" bug: at which frame `messages`
 * populates, `hasReached` turns true, and the settled-guard is written, aligned
 * with Virtuoso's `totalListHeightChanged` measurement chronology. Local repro is
 * blocked (dev-proxy auth cookie), so this ships behind an explicit opt-in for a
 * one-off real-device capture. Remove this file and its call sites with the
 * successor fix — it is NOT a product API.
 *
 * Opt-in: set `window.__bssTraceEnabled = true` in the SPA window BEFORE opening
 * the channel. Default path (flag unset) is zero cost: no array, no push, no
 * `performance.now()`. First-visit trace is bounded to keep it a fixed, one-shot
 * sample. Records only numbers/booleans/short kinds — no message content, tokens,
 * or raw identifiers.
 */
import type { ChannelMessagesPage } from "@multica/core/types";

const MAX_ENTRIES = 200;

/**
 * DIAGNOSTIC ONLY — three-state source-tail-complete for the trace, so it never
 * conflates "latest page not returned yet" with "tail complete" (the first link
 * in the data-ready vs measurement-lag causal chain the trace must classify):
 *   - no page loaded          -> null  (unknown — data not ready yet)
 *   - loaded, has_more_after   -> false (newer messages exist beyond the window)
 *   - loaded, tail in window   -> true  (`has_more_after` absent = the
 *                                        default/before-cursor page, which IS the tail)
 * Remove with the successor fix.
 */
export function deriveDiagSourceTailComplete(
  firstPage: ChannelMessagesPage | undefined,
): boolean | null {
  if (!firstPage) return null;
  return !firstPage.has_more_after;
}

interface BssTraceWindow {
  __bssTraceEnabled?: boolean;
  __bssTrace?: Array<Record<string, unknown>>;
}

function traceWindow(): BssTraceWindow | null {
  if (typeof window === "undefined") return null;
  return window as unknown as BssTraceWindow;
}

/** True when the opt-in flag is set — callers can skip building record args. */
export function bssTraceEnabled(): boolean {
  const w = traceWindow();
  return !!w?.__bssTraceEnabled;
}

/** Append one bounded trace entry when opted in; otherwise a no-op. */
export function bssRecord(kind: string, fields: Record<string, number | boolean | string | null>): void {
  const w = traceWindow();
  if (!w?.__bssTraceEnabled) return;
  const arr = (w.__bssTrace ??= []);
  if (arr.length >= MAX_ENTRIES) return;
  arr.push({ t: performance.now(), kind, ...fields });
}
