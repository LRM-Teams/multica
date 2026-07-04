import type { TimelineEntry } from "@multica/core/types";

// The one comment_type that is a real, conversational Message — the only kind
// that may render as a reactable/quotable/copyable/searchable bubble. Every
// other comment_type (status_change / progress_update / system) is an
// execution-result row and must live in the Activity lane, never a Message row
// (#157 / E1 / H4).
const REACTABLE_COMMENT_TYPE = "comment";

/**
 * True when the entry must render in the Activity lane rather than as a
 * reactable Message bubble. Covers real `activity` entries AND comments whose
 * `comment_type` is an execution result (status_change / progress_update /
 * system). Isolating these here is the #243 guarantee that an execution result
 * is never a Message row and never an empty row.
 */
export function isActivityLaneEntry(entry: TimelineEntry): boolean {
  if (entry.type === "activity") return true;
  return entry.comment_type != null && entry.comment_type !== REACTABLE_COMMENT_TYPE;
}

/**
 * True only for a real conversational comment — the sole entry kind that may
 * render as a reactable/quotable Message bubble. A comment with no
 * `comment_type` is treated as a real comment (safe default).
 */
export function isReactableComment(entry: TimelineEntry): boolean {
  return entry.type === "comment" && !isActivityLaneEntry(entry);
}

// Pointer keys we accept from an entry's `details`, in priority order. The
// #236 Activity lane owns the canonical name; we read defensively so a pointer
// arriving under any of these still renders as an explicit link rather than an
// empty row.
const RUN_POINTER_KEYS = ["activity_run_id", "run_id", "task_id"] as const;

/**
 * Extract an explicit Activity-run pointer id (#236) from an entry's `details`,
 * or null when the entry carries none. Used to render an explicit link to the
 * Activity run instead of an empty message row.
 */
export function activityRunPointer(entry: TimelineEntry): string | null {
  const details = entry.details;
  if (!details) return null;
  for (const key of RUN_POINTER_KEYS) {
    const value = details[key];
    if (typeof value === "string" && value.length > 0) return value;
  }
  return null;
}
