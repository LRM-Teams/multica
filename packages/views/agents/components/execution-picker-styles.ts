/**
 * Shared density for execution-config pickers (Computer / Runtime / Model /
 * Reasoning). Match `Input` (h-8, border-input) so a stack of four fields
 * reads as form controls, not oversized “cards”.
 *
 * Closed trigger is single-line; secondary detail lives in the menu only.
 *
 * `overflow-hidden` on the trigger is required for long model IDs: a flex
 * `<button>` will otherwise grow past its parent even when the label has
 * `truncate` (button min-content sizing + DialogContent grid min-width:auto).
 */
export const executionFieldClass = "flex min-w-0 flex-col gap-1.5";

export const executionTriggerClass =
  "flex h-8 w-full min-w-0 max-w-full overflow-hidden items-center gap-2 rounded-lg border border-input bg-transparent px-2.5 text-left text-sm outline-none transition-colors hover:bg-muted/40 focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30";

export const executionOptionClass =
  "flex w-full min-w-0 items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-sm transition-colors hover:bg-accent/50";

export const executionOptionSelectedClass = "bg-accent";
