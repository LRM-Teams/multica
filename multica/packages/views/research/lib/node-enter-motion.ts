/**
 * LRM-827 — canvas / strip node enter motion.
 * Duration stays ≤300ms; transform targets the card shell (not RF position).
 */

export const NODE_ENTER_CLASS = "research-node-enter";
/** Per-node enter animation length (AC: ≤300ms). */
export const NODE_ENTER_DURATION_MS = 280;
/** Delay between consecutive new nodes in one batch. */
export const NODE_ENTER_STAGGER_MS = 40;
/** Cap stagger steps so a large batch does not trail forever. */
export const NODE_ENTER_STAGGER_CAP = 10;

export const NODE_ENTER_DELAY_VAR = "--research-node-enter-delay";

/** Stagger delay for the Nth new node in the current enter batch (0-based). */
export function nodeEnterStaggerDelayMs(batchIndex: number): number {
  if (batchIndex < 0) return 0;
  return Math.min(batchIndex, NODE_ENTER_STAGGER_CAP) * NODE_ENTER_STAGGER_MS;
}

/** Inline style snippet that feeds animation-delay via CSS variable. */
export function nodeEnterDelayStyle(delayMs: number): Record<string, string> {
  return {
    [NODE_ENTER_DELAY_VAR]: `${Math.max(0, delayMs)}ms`,
  };
}

/** Shared keyframes for desktop RF nodes + narrow logic strip cards. */
export function nodeEnterMotionCss(): string {
  return `
.${NODE_ENTER_CLASS} .research-graph-node-shell,
.${NODE_ENTER_CLASS}.research-logic-strip-card-enter {
  animation: research-node-enter ${NODE_ENTER_DURATION_MS}ms cubic-bezier(0.22, 1, 0.36, 1) both;
  animation-delay: var(${NODE_ENTER_DELAY_VAR}, 0ms);
}
@keyframes research-node-enter {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
@media (prefers-reduced-motion: reduce) {
  .${NODE_ENTER_CLASS} .research-graph-node-shell,
  .${NODE_ENTER_CLASS}.research-logic-strip-card-enter {
    animation: none;
  }
}
`;
}
