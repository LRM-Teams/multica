/**
 * Channel agent shells are titled "#channelName" with no whitespace
 * (see ensureChannelAgentSession). Bubble / DM history must stay 1:1, so any
 * title that looks like that shell is treated as channel-origin — even when
 * the live channel list is empty, stale, or the channel was renamed/deleted.
 *
 * Keeps titles like "PR #1217 follow-up" (space before # / mixed prose).
 */
const CHANNEL_SHELL_TITLE = /^#[^\s]+$/;

export function isChannelShellSessionTitle(title: string | null | undefined): boolean {
  if (!title) return false;
  return CHANNEL_SHELL_TITLE.test(title.trim());
}

export function excludeChannelShellSessions<T extends { title: string }>(sessions: T[]): T[] {
  return sessions.filter((session) => !isChannelShellSessionTitle(session.title));
}
