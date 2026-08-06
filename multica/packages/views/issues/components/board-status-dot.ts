import type { IssueStatus } from "@multica/core/types";

// Board column status dot — a flat filled (or hollow) circle in the status
// color, matching the kanban design. Lives in this sibling module (not the
// board-column component file) so read-only surfaces — the group Tasks panel
// (#562) — can reuse the exact same status→color mapping without importing the
// store/drag-coupled board column, and without tripping react-doctor's
// non-component-export rule. Uses semantic color tokens only (no hardcoded
// Tailwind colors). Kept distinct from the list/swimlane StatusIcon (a detailed
// progress glyph) on purpose.
export const BOARD_STATUS_DOT: Record<IssueStatus, string> = {
  backlog: "border-[1.5px] border-muted-foreground/40",
  todo: "border-[1.5px] border-muted-foreground/40",
  in_progress: "bg-brand",
  in_review: "bg-warning",
  done: "bg-muted-foreground/40",
  blocked: "bg-destructive",
  cancelled: "bg-muted-foreground/40",
};
