/**
 * #808 — per-channel dismissal for the group-manager onboarding hint. Local and
 * best-effort: a hint the user waved away should not come back, but losing that
 * memory (private mode, cleared storage) only means seeing a dismissible hint
 * again, so storage failures are swallowed rather than surfaced.
 *
 * Lives in its own module so the component file exports a component only
 * (react-refresh/only-export-components).
 */
const KEY_PREFIX = "multica:channels:group-manager-hint-dismissed:";

export function readGroupManagerHintDismissed(channelId: string): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(KEY_PREFIX + channelId) === "1";
  } catch {
    return false;
  }
}

export function dismissGroupManagerHint(channelId: string): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(KEY_PREFIX + channelId, "1");
  } catch {
    // Ignore — dismissal is a convenience, not a correctness requirement.
  }
}
