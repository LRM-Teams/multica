import { onlineManager } from "@tanstack/react-query";

let installed = false;

/**
 * LRM-844 — cold-start desktop DM list can stick on the skeleton forever when
 * React Query's onlineManager flips to offline (spurious Chromium `offline`
 * under heavy first paint) and the matching `online` event is missed. Queries
 * with `networkMode: "online"` then sit at `pending` + paused with observers,
 * and `isPending` keeps painting `[data-testid="dm-list-skeleton"]`.
 *
 * Recovery: keep the default online/offline listeners, and optimistically
 * re-assert online on focus / visibility. A truly offline network still fails
 * the fetch; it no longer leaves the shell hung with no retry path.
 */
export function installQueryOnlineRecovery(): void {
  if (installed) return;
  installed = true;

  // Clear any stale false left by HMR / a prior listener race before mount.
  // Also runs in Node test envs (no window) so createQueryClient cannot leave
  // onlineManager latched false across vitest cases.
  onlineManager.setOnline(true);

  if (typeof window === "undefined") return;

  onlineManager.setEventListener((setOnline) => {
    const goOnline = () => setOnline(true);
    const goOffline = () => setOnline(false);
    const resume = () => setOnline(true);
    const onVisibility = () => {
      if (document.visibilityState === "visible") resume();
    };

    window.addEventListener("online", goOnline);
    window.addEventListener("offline", goOffline);
    window.addEventListener("focus", resume);
    document.addEventListener("visibilitychange", onVisibility);
    resume();

    return () => {
      window.removeEventListener("online", goOnline);
      window.removeEventListener("offline", goOffline);
      window.removeEventListener("focus", resume);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  });
}

/** Test-only: allow re-installing the listener across vitest cases. */
export function resetQueryOnlineRecoveryForTests(): void {
  installed = false;
}
