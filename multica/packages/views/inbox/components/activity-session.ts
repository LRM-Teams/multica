import type { UserActivityItem } from "@multica/core/types";

/**
 * LRM-388 — how Activity should open a thread-shaped row in the right pane.
 *
 * - `dm`: reuse DmConversation (Chat tab default lives inside that shell)
 * - `thread`: reuse ThreadPanel (replying thread / followed / participated)
 * - `channel`: full channel stream scrolled to the trigger message (bare
 *   channel mention with no thread replies yet — not a fragment card)
 */
export type ActivitySessionSurface = "dm" | "thread" | "channel";

export function resolveActivitySessionSurface(
  item: Pick<
    UserActivityItem,
    "kind" | "channel_kind" | "reply_count" | "followed" | "participated"
  >,
): ActivitySessionSurface | null {
  if (item.kind !== "thread") return null;
  if (item.channel_kind === "dm") return "dm";

  const replyCount = item.reply_count ?? 0;
  if (replyCount > 0 || item.followed === true || item.participated === true) {
    return "thread";
  }
  return "channel";
}

export type ActivitySessionUrl = {
  tab?: "all" | "unread" | "mentions";
  issue?: string;
  channel?: string;
  thread?: string;
  message?: string;
};

/**
 * Build Activity URL search params for a selected feed row.
 * Issue rows keep `issue=`; thread rows use `channel` + `thread` or `message`
 * (never both thread and message — surface owns scroll target).
 */
export function activitySessionParams(
  item: UserActivityItem,
  tab: ActivitySessionUrl["tab"] = "all",
): ActivitySessionUrl {
  const base: ActivitySessionUrl = tab && tab !== "all" ? { tab } : {};

  if (item.kind === "inbox") {
    const inbox = item.inbox;
    const issue = inbox?.issue_id ?? inbox?.id ?? item.id;
    return { ...base, issue };
  }

  const channel = item.channel_id ?? undefined;
  if (!channel) return base;

  const rootId = item.thread_root_message_id ?? item.id;
  const surface = resolveActivitySessionSurface(item);
  if (surface === "channel") {
    return { ...base, channel, message: rootId };
  }
  // dm + thread both deep-link the thread root; DmConversation / ThreadPanel
  // consume `thread=` the same way ChannelsPage does on /channels/[id].
  return { ...base, channel, thread: rootId };
}

export function activitySessionUrl(
  inboxPath: string,
  params: ActivitySessionUrl,
): string {
  const search = new URLSearchParams();
  if (params.tab && params.tab !== "all") search.set("tab", params.tab);
  if (params.issue) search.set("issue", params.issue);
  if (params.channel) search.set("channel", params.channel);
  if (params.thread) search.set("thread", params.thread);
  if (params.message) search.set("message", params.message);
  const qs = search.toString();
  return qs ? `${inboxPath}?${qs}` : inboxPath;
}

export function activitySelectionKey(item: UserActivityItem): string {
  if (item.kind === "inbox") {
    const inbox = item.inbox;
    return inbox?.issue_id ?? inbox?.id ?? item.id;
  }
  return item.id;
}

/**
 * Match a feed row to the Activity deep-link selection key (`issue` /
 * `thread` / `message` query value). Thread rows may deep-link via
 * `thread_root_message_id` while `id` is the same root (or equal) — accept
 * either so URL ↔ row resolution stays stable after unread mark-read.
 */
export function activityItemMatchesSelection(
  item: UserActivityItem,
  selectedKey: string,
): boolean {
  if (!selectedKey) return false;
  if (activitySelectionKey(item) === selectedKey) return true;
  if (item.kind === "thread") {
    return (
      item.id === selectedKey || item.thread_root_message_id === selectedKey
    );
  }
  if (item.kind === "inbox") {
    const inbox = item.inbox;
    return (
      item.id === selectedKey ||
      inbox?.id === selectedKey ||
      inbox?.issue_id === selectedKey
    );
  }
  return false;
}
