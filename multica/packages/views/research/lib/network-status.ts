/** LRM-833 — 5xx from API client (duck-typed; avoids hard ApiError import for tests). */
export function isServerError(error: unknown): boolean {
  if (!error || typeof error !== "object") return false;
  const status = (error as { status?: unknown }).status;
  return typeof status === "number" && status >= 500;
}

/** Browser online flag; SSR / missing navigator → assume online. */
export function readBrowserOnline(): boolean {
  if (typeof navigator === "undefined") return true;
  return navigator.onLine !== false;
}

export type ReconnectPhase = "idle" | "reconnecting" | "failed";

export type OfflineBannerMode = "offline" | "reconnecting" | "failed";

/** Resolve banner chrome from online flag + reconnect phase. */
export function resolveOfflineBannerMode(
  online: boolean,
  phase: ReconnectPhase,
): OfflineBannerMode | null {
  if (!online) return "offline";
  if (phase === "reconnecting") return "reconnecting";
  if (phase === "failed") return "failed";
  return null;
}
