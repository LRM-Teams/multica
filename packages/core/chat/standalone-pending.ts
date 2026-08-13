/**
 * Standalone Agent Chat (FAB / isolated bubble) treats an outstanding
 * reply as a session fact keyed by chat_session_id. There is no Task
 * and no inbox event on this path.
 */

export function isStandaloneSendOutstanding(
  send: { pending?: boolean } | null | undefined,
): boolean {
  return send?.pending === true;
}

export function isStandaloneSessionOutstanding(
  pending: { pending?: boolean } | null | undefined,
): boolean {
  return pending?.pending === true;
}

export function isStandaloneListItemOutstanding(
  item: { pending?: boolean; chat_session_id?: string } | null | undefined,
): boolean {
  return !!item?.chat_session_id && item.pending === true;
}

/** chat:done is session-scoped. Always clear this session's outstanding. */
export function shouldClearChatPendingOnDone(): boolean {
  return true;
}

export function standaloneStopRequiresInbox(): boolean {
  return false;
}
