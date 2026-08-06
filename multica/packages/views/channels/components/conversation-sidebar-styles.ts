/**
 * LRM-354 / LRM-551 — shared Messages sidebar (desktop + mobile list) surface
 * tokens on the `bg-sidebar` chrome plane.
 *
 * Selected: light = --sidebar-accent → surface; dark = --sidebar-accent lift
 * (never a washed white block / primary opacity wash).
 * Idle hover: sidebar-accent (not accent — accent is eaten by sidebar bg).
 * Collapsed-section unread: brand + brand-foreground (≥4.5:1).
 * Section headers stay on callers as text-muted-foreground.
 */

/** Active channel / DM row fill. */
export const CONVERSATION_SIDEBAR_ROW_ACTIVE = "bg-sidebar-accent";

/** Idle row hover — must stay readable on bg-sidebar (LRM-551 / lock A). */
export const CONVERSATION_SIDEBAR_ROW_IDLE = "hover:bg-sidebar-accent";

/** Collapsed PINNED / DMs / CHANNELS aggregate unread pill. */
export const CONVERSATION_SIDEBAR_UNREAD_BADGE =
  "flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-brand-solid px-1 text-[10px] font-semibold text-brand-solid-foreground";
