/**
 * Shared width for the docked conversation side panel (agent Profile /
 * thread / channel details) on desktop group Chat.
 *
 * Restored after LRM-388/400 swapped the inner ResizablePanel for a fixed
 * flex column (to keep the chat column mounted and avoid a blank half-pane).
 * Width is remembered in localStorage across refresh; mobile does not use
 * this path (full-width drawer / single column).
 */

export const CONVERSATION_SIDE_PANEL_MIN_PX = 300;
export const CONVERSATION_SIDE_PANEL_MAX_PX = 480;
export const CONVERSATION_SIDE_PANEL_DEFAULT_PX = 360;
/** Cap as a fraction of the conversation+side container width. */
export const CONVERSATION_SIDE_PANEL_MAX_RATIO = 0.45;

export const CONVERSATION_SIDE_PANEL_WIDTH_STORAGE_KEY =
  "multica:conversation-side-panel-width";

export function clampConversationSidePanelWidth(
  width: number,
  containerWidth?: number,
): number {
  let max = CONVERSATION_SIDE_PANEL_MAX_PX;
  if (containerWidth != null && containerWidth > 0) {
    max = Math.min(
      max,
      Math.floor(containerWidth * CONVERSATION_SIDE_PANEL_MAX_RATIO),
    );
  }
  // Keep min ≤ max when the container is extremely narrow.
  const min = Math.min(CONVERSATION_SIDE_PANEL_MIN_PX, max);
  return Math.max(min, Math.min(max, Math.round(width)));
}

export function readConversationSidePanelWidth(): number {
  if (typeof window === "undefined") {
    return CONVERSATION_SIDE_PANEL_DEFAULT_PX;
  }
  try {
    const raw = window.localStorage.getItem(
      CONVERSATION_SIDE_PANEL_WIDTH_STORAGE_KEY,
    );
    if (raw == null) return CONVERSATION_SIDE_PANEL_DEFAULT_PX;
    const parsed = Number(raw);
    if (!Number.isFinite(parsed)) return CONVERSATION_SIDE_PANEL_DEFAULT_PX;
    return clampConversationSidePanelWidth(parsed);
  } catch {
    return CONVERSATION_SIDE_PANEL_DEFAULT_PX;
  }
}

export function writeConversationSidePanelWidth(width: number): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(
      CONVERSATION_SIDE_PANEL_WIDTH_STORAGE_KEY,
      String(clampConversationSidePanelWidth(width)),
    );
  } catch {
    // Private mode / quota — drag still works in-session; persistence is best-effort.
  }
}
