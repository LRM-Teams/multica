/**
 * Whether a chat list should jump to the latest row after a tail change.
 *
 * First populate of a session lands on the latest. A newly appended user
 * message always pins, even if the reader had scrolled up. Assistant
 * replies only follow if the list is already pinned (handled by Virtuoso
 * followOutput / near-bottom, not this helper).
 */
export function shouldPinChatToLatest(
  previousTailId: string | undefined,
  nextTail: { id: string; role: string } | undefined,
): boolean {
  if (!nextTail) return false;
  if (previousTailId === nextTail.id) return false;
  if (previousTailId === undefined) return true;
  return nextTail.role === "user";
}
