/**
 * Messages conversation list (desktop + mobile list) lives on the product
 * surface (`bg-background`), not the app-sidebar chrome plane. The two left
 * columns must stay distinct so nav + list don't read as one slab.
 *
 * Selected: muted (page-bg) — visible on surface. Never primary opacity wash.
 * Idle hover: accent (--hover) — the frozen row-hover token.
 * Collapsed-section unread: brand + brand-foreground (≥4.5:1).
 * Section headers stay on callers as text-muted-foreground.
 */

/** Conversation list column — surface plane, distinct from app sidebar. */
export const CONVERSATION_LIST_PANE_BG = "bg-background";

/** Active channel / DM row fill. */
export const CONVERSATION_SIDEBAR_ROW_ACTIVE = "bg-muted";

/** Idle row hover — readable on the surface list plane. */
export const CONVERSATION_SIDEBAR_ROW_IDLE = "hover:bg-accent";

/** Collapsed PINNED / DMs / CHANNELS aggregate unread pill. */
export const CONVERSATION_SIDEBAR_UNREAD_BADGE =
  "flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-brand-solid px-1 text-[10px] font-semibold text-brand-solid-foreground";
