/**
 * LRM-1472 / UI-04 — discrete keyboard navigation within the dispute subgraph.
 * Positions / turns / decisions are roving-tabindex rows; arrow keys move an
 * index across them. Pure function over a linear order so it stays trivially
 * testable and never touches the canvas graph itself (display-only).
 */

export type DisputeNavDirection = "next" | "prev";

/**
 * Move the cursor within a linear roster, clamping at the ends. Rows are the
 * fan positions, turning, and decision history entries presented in DOM order.
 */
export function moveDisputeNavIndex(
  index: number,
  length: number,
  direction: DisputeNavDirection,
): number {
  if (length <= 0) return -1;
  const delta = direction === "next" ? 1 : -1;
  return Math.max(0, Math.min(length - 1, index + delta));
}

export type RovingTabParams = {
  /** index of the currently focused row, or -1 when none. */
  focusedIndex: number;
  length: number;
  direction: DisputeNavDirection;
};

/** Arrow-key result: the new focus index and whether it changed. */
export function rovingTabNext({ focusedIndex, length, direction }: RovingTabParams): {
  index: number;
  changed: boolean;
} {
  if (length <= 0) return { index: -1, changed: false };
  const base = focusedIndex < 0 ? (direction === "next" ? -1 : length) : focusedIndex;
  const index = moveDisputeNavIndex(base, length, direction);
  return { index, changed: index !== focusedIndex };
}

/** Keyboard key → nav direction, or null when not a dispute-nav key. */
export function disputeNavFromKey(key: string): DisputeNavDirection | null {
  switch (key) {
    case "ArrowDown":
    case "ArrowRight":
      return "next";
    case "ArrowUp":
    case "ArrowLeft":
      return "prev";
    default:
      return null;
  }
}
