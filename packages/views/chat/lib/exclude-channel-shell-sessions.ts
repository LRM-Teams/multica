/**
 * Channel agent shells are titled "#channelName" (see ensureChannelAgentSession).
 * ListChatSessions already drops channel_agent_session rows server-side, but
 * orphan titles and a stale TanStack cache can still surface them in the
 * agent bubble history. Mirror the server heuristic on the client.
 */
export function isChannelShellSessionTitle(
  title: string | null | undefined,
  channelNames: Iterable<string>,
): boolean {
  if (!title || !title.startsWith("#")) return false;
  const name = title.slice(1);
  if (!name) return false;
  for (const channelName of channelNames) {
    if (channelName === name) return true;
  }
  return false;
}

export function excludeChannelShellSessions<T extends { title: string }>(
  sessions: T[],
  channelNames: Iterable<string>,
): T[] {
  const names = channelNames instanceof Set ? channelNames : new Set(channelNames);
  if (names.size === 0) return sessions;
  return sessions.filter((session) => !isChannelShellSessionTitle(session.title, names));
}
