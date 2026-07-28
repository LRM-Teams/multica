import { toast } from "sonner";

/**
 * Presentation extras a failure may carry. Deliberately NOT sonner's full
 * `ExternalToast`: `duration`, `closeButton` and friends are the invariant this
 * helper owns, and re-exposing them would let a call site opt back out of it.
 */
export interface ErrorToastOptions {
  /** Secondary line under the message — the specific cause, not a restatement. */
  description?: string;
}

/**
 * #835/#839 — the one place a failure is announced.
 *
 * sonner's defaults (`TOAST_LIFETIME` 4s, 3 visible) were never chosen by us:
 * every failure message we show auto-dismisses after four seconds, and a fourth
 * toast silently evicts an unresolved one. For a failure that is wrong — a
 * failure is unresolved state, and letting it age out recreates the "looks
 * fine" problem we spent #1276 removing. So an error toast persists until the
 * user dismisses it, and always carries a visible close control.
 *
 * `toastOptions` on `<Toaster>` cannot express this: it is a single global
 * config with no per-type override, so making errors persistent there would
 * also pin every success toast on screen. Hence one shared call site instead.
 *
 * IMPORTANT: this is the ANNOUNCEMENT, not the record. A dismissed toast must
 * not be the only trace of a failure — the failing surface still owes a durable
 * state (inline failed row, retry affordance, …) that survives dismissal.
 */
export function showErrorToast(
  message: string,
  options?: ErrorToastOptions,
): void {
  // The persistence guarantee is applied AFTER the caller's options on purpose:
  // `duration`/`closeButton` are the whole reason this helper exists, so a call
  // site must not be able to hand back a 4s-and-gone error by passing its own
  // `duration`. Presentation extras (a secondary `description` line) are the
  // caller's to choose; the invariant is not.
  toast.error(message, { ...options, duration: Infinity, closeButton: true });
}
