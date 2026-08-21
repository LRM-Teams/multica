export type ChatWindowLayout = "floating" | "fullscreen" | "sidebar";

/** Default Notes assistant rail (24rem). Drag persists a pixel width. */
export const CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH = 384;
export const CHAT_WINDOW_SIDEBAR_MIN_WIDTH = 280;
export const CHAT_WINDOW_SIDEBAR_MAX_WIDTH = 640;
export const CHAT_WINDOW_SIDEBAR_WIDTH_STORAGE_KEY = "multica:chat:noteBubbleSidebarWidth";

export function clampChatWindowSidebarWidth(width: number): number {
  if (!Number.isFinite(width)) return CHAT_WINDOW_SIDEBAR_DEFAULT_WIDTH;
  return Math.min(
    CHAT_WINDOW_SIDEBAR_MAX_WIDTH,
    Math.max(CHAT_WINDOW_SIDEBAR_MIN_WIDTH, Math.round(width)),
  );
}

export function chatWindowShellClassName(layout: ChatWindowLayout): string {
  if (layout === "fullscreen") {
    return "fixed inset-0 z-50 flex h-dvh max-h-dvh w-full flex-row bg-background overflow-hidden pt-[env(safe-area-inset-top,0px)]";
  }
  if (layout === "sidebar") {
    // Absolute inside the viewport clip — not `fixed` (fixed children ignore
    // parent overflow) and not a width class (closed rail must not paint a
    // 24rem blank column over the note scrollbar).
    return "absolute inset-y-0 right-0 z-50 flex h-full max-w-[90vw] flex-row border-l bg-background shadow-xl overflow-hidden transition-transform duration-200 ease-out motion-reduce:transition-none";
  }
  return "absolute bottom-2 right-2 z-50 flex flex-row rounded-xl ring-1 ring-foreground/10 bg-sidebar shadow-2xl overflow-hidden";
}

/** Viewport clip so a closed/off-screen rail cannot widen document overflow. */
export function chatWindowSidebarClipClassName(): string {
  return "pointer-events-none fixed inset-0 z-50 overflow-hidden";
}

/** CSS slide — sidebar is a plain `div`. Motion `x` / `initial={false}`
 * writes `transform: none` and would pin a closed rail on screen. */
export function chatWindowSidebarSlideClassName(open: boolean): string {
  return open ? "translate-x-0" : "translate-x-full";
}

export const NOTE_ASSISTANT_SIDEBAR_EXIT_MS = 200;

export type NoteAssistantSidebarPresence = "omit" | "closed" | "open";

/** Closed + never (or no longer) mounted → no rail in the DOM. */
export function noteAssistantSidebarPresence(
  open: boolean,
  stayMounted: boolean,
): NoteAssistantSidebarPresence {
  if (open) return "open";
  if (stayMounted) return "closed";
  return "omit";
}

export function chatWindowUsesFloatingChrome(layout: ChatWindowLayout): boolean {
  return layout === "floating";
}

export function chatWindowClosesOnOutsideClick(layout: ChatWindowLayout): boolean {
  return layout === "floating";
}
