/**
 * LRM-354 — conversation list (channel / DM) selected + idle hover washes.
 *
 * Never use `bg-primary/α`: in dark mode `--primary` is near-white, so
 * `bg-primary/[0.08]` becomes a washed light slab on the sidebar. Use the
 * shared `accent` / `hover` semantic tokens instead (maps to `--hover` /
 * `--sidebar-accent` family — readable on both themes).
 */
export const CONVERSATION_LIST_ROW_SELECTED_CLASS = "bg-accent";
export const CONVERSATION_LIST_ROW_IDLE_CLASS = "hover:bg-accent/70";

/**
 * Collapsed-section aggregate unread + Activity nav count — brand fill so
 * light and dark both clear ≥4.5:1 (never `bg-primary`, which flips to ink
 * wash / near-white in the opposite theme).
 */
export const SIDEBAR_UNREAD_COUNT_BADGE_CLASS =
  "flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-brand px-1 text-[10px] font-semibold text-brand-foreground";
