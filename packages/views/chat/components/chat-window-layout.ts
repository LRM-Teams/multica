export type ChatWindowLayout = "floating" | "fullscreen" | "sidebar";

export type ChatWindowMainPane = "skeleton" | "messages" | "empty" | "spacer";

/**
 * Message-area slot. Composer chips always sit above the input — they
 * never replace this pane. An empty thread with chips uses a flex
 * spacer so the input stays pinned to the bottom.
 */
export function chatWindowMainPane(
  showSkeleton: boolean,
  hasMessages: boolean,
  hasComposerAccessory: boolean,
): ChatWindowMainPane {
  if (showSkeleton) return "skeleton";
  if (hasMessages) return "messages";
  if (hasComposerAccessory) return "spacer";
  return "empty";
}

export function chatWindowMainPaneClassName(pane: ChatWindowMainPane): string {
  return pane === "spacer" ? "min-h-0 flex-1" : "";
}

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

/** Leaving this note (or unmounting its bubble) closes that page's rail. */
export function noteAssistantSidebarClosesOnLeave(
  openPageId: string | null,
  leavingPageId: string,
): boolean {
  return openPageId === leavingPageId;
}

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

/** Notes rail (desktop) and the mobile notes sheet. Not the global FAB card. */
export function chatWindowClosesOnEscape(layout: ChatWindowLayout): boolean {
  return layout === "sidebar" || layout === "fullscreen";
}

/**
 * Portals that consume Escape themselves (dialog, menu, session popover).
 * Tooltips are omitted — they must not block closing the rail.
 */
export const CHAT_WINDOW_ESCAPE_LAYER_OWNERS = [
  '[data-slot="popover-content"]',
  '[data-slot="dialog-content"]',
  '[data-slot="dialog-overlay"]',
  '[data-slot="alert-dialog-content"]',
  '[data-slot="alert-dialog-overlay"]',
  '[data-slot="dropdown-menu-content"]',
  '[data-slot="dropdown-menu-sub-content"]',
  '[data-slot="select-content"]',
  '[data-slot="sheet-content"]',
  '[data-slot="sheet-overlay"]',
] as const;

export function chatWindowEscapeLayerSelector(): string {
  return CHAT_WINDOW_ESCAPE_LAYER_OWNERS.join(",");
}

/** True when this Escape should minimize the notes bubble (same as the header Minus). */
export function chatWindowEscapeClosesNoteBubble(
  event: Pick<KeyboardEvent, "key" | "defaultPrevented">,
  root: ParentNode | null = typeof document === "undefined" ? null : document,
): boolean {
  if (event.key !== "Escape" || event.defaultPrevented) return false;
  if (!root) return true;
  return root.querySelector(chatWindowEscapeLayerSelector()) === null;
}

/** Width the Notes page must reserve so the editor recenters beside the rail. */
export function noteAssistantSidebarReservePx(
  open: boolean,
  isMobile: boolean,
  width: number,
): number {
  if (!open || isMobile) return 0;
  return clampChatWindowSidebarWidth(width);
}
