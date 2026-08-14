// Layout-only labels for compact non-Runner surfaces. Fine-grained Agent
// Activity labels always arrive from the server projection.
export const RUNNER_ACTIVITY_LABEL_EN = {
  idle: "Idle",
  working: "Working",
  waiting: "Waiting",
} as const;

/** Matches the server-owned command label, including its in-progress suffix. */
export function isRunningCommandActivityLabel(label: string | null | undefined): boolean {
  return /^Running command(?:[.…]+)?$/u.test(label?.trim() ?? "");
}
