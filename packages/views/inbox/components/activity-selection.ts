import type { UserActivityItem, UserActivityTab } from "@multica/core/types";

/**
 * Right-pane selection for Activity (LRM-388). URL-backed so refresh/share
 * restores the same pane without leaving `/inbox`.
 *
 * Mutually exclusive query keys (plus optional `tab`):
 * - `issue` → inbox / IssueDetail
 * - `channel` + `thread` → ThreadPanel
 * - `channel` + `message` → full group channel stream (highlight target)
 * - `dm` (+ optional `message` / `thread`) → DmConversation
 */
export type ActivityPaneKind = "inbox" | "thread" | "channel" | "dm";

export type ActivitySelection =
  | { kind: "inbox"; key: string }
  | { kind: "thread"; channelId: string; threadRootId: string }
  | { kind: "channel"; channelId: string; messageId: string }
  | { kind: "dm"; channelId: string; messageId?: string; threadRootId?: string };

export function parseActivityTab(raw: string | null): UserActivityTab {
  if (raw === "unread" || raw === "mentions") return raw;
  return "all";
}

export function parseActivitySelection(
  params: URLSearchParams | { get: (key: string) => string | null },
): ActivitySelection | null {
  const issue = params.get("issue");
  if (issue) return { kind: "inbox", key: issue };

  const dm = params.get("dm");
  if (dm) {
    const messageId = params.get("message") ?? undefined;
    const threadRootId = params.get("thread") ?? undefined;
    return {
      kind: "dm",
      channelId: dm,
      ...(messageId ? { messageId } : {}),
      ...(threadRootId ? { threadRootId } : {}),
    };
  }

  const channelId = params.get("channel");
  if (!channelId) return null;

  const threadRootId = params.get("thread");
  if (threadRootId) {
    return { kind: "thread", channelId, threadRootId };
  }

  const messageId = params.get("message");
  if (messageId) {
    return { kind: "channel", channelId, messageId };
  }

  return null;
}

export function activitySelectionEquals(
  a: ActivitySelection | null,
  b: ActivitySelection | null,
): boolean {
  if (a === b) return true;
  if (!a || !b || a.kind !== b.kind) return false;
  switch (a.kind) {
    case "inbox":
      return b.kind === "inbox" && a.key === b.key;
    case "thread":
      return (
        b.kind === "thread" &&
        a.channelId === b.channelId &&
        a.threadRootId === b.threadRootId
      );
    case "channel":
      return (
        b.kind === "channel" &&
        a.channelId === b.channelId &&
        a.messageId === b.messageId
      );
    case "dm":
      return (
        b.kind === "dm" &&
        a.channelId === b.channelId &&
        (a.messageId ?? "") === (b.messageId ?? "") &&
        (a.threadRootId ?? "") === (b.threadRootId ?? "")
      );
  }
}

export function inboxActivityUrl(
  inboxPath: string,
  params: {
    tab?: UserActivityTab;
    selection?: ActivitySelection | null;
  },
): string {
  const search = new URLSearchParams();
  if (params.tab && params.tab !== "all") search.set("tab", params.tab);
  const selection = params.selection ?? null;
  if (selection) {
    switch (selection.kind) {
      case "inbox":
        search.set("issue", selection.key);
        break;
      case "thread":
        search.set("channel", selection.channelId);
        search.set("thread", selection.threadRootId);
        break;
      case "channel":
        search.set("channel", selection.channelId);
        search.set("message", selection.messageId);
        break;
      case "dm":
        search.set("dm", selection.channelId);
        if (selection.messageId) search.set("message", selection.messageId);
        if (selection.threadRootId) search.set("thread", selection.threadRootId);
        break;
    }
  }
  const qs = search.toString();
  return qs ? `${inboxPath}?${qs}` : inboxPath;
}

/**
 * Which right-pane shell an Activity row opens (LRM-374 / LRM-388).
 *
 * Activity only exposes `thread` | `inbox` kinds. Group thread rows with
 * replies open the Thread panel; top-level group mentions with no replies
 * open the full channel stream scrolled to that message; DMs open the DM
 * Chat surface. Explicit — no silent swap between shells.
 */
export function resolveActivityPaneKind(
  item: UserActivityItem,
): ActivityPaneKind | null {
  if (item.kind === "inbox") return "inbox";
  if (item.kind !== "thread") return null;
  if (item.channel_kind === "dm") return "dm";
  if ((item.reply_count ?? 0) > 0) return "thread";
  return "channel";
}

export function selectionFromActivityItem(
  item: UserActivityItem,
): ActivitySelection | null {
  if (item.access_denied) return null;

  const pane = resolveActivityPaneKind(item);
  if (!pane) return null;

  if (pane === "inbox") {
    const inbox = item.inbox;
    if (!inbox) return null;
    return { kind: "inbox", key: inbox.issue_id ?? inbox.id };
  }

  const channelId = item.channel_id;
  const rootId = item.thread_root_message_id ?? item.id;
  if (!channelId || !rootId) return null;

  if (pane === "dm") {
    return { kind: "dm", channelId, messageId: rootId };
  }
  if (pane === "thread") {
    return { kind: "thread", channelId, threadRootId: rootId };
  }
  return { kind: "channel", channelId, messageId: rootId };
}

export function activitySelectionKey(selection: ActivitySelection): string {
  switch (selection.kind) {
    case "inbox":
      return `inbox:${selection.key}`;
    case "thread":
      return `thread:${selection.channelId}:${selection.threadRootId}`;
    case "channel":
      return `channel:${selection.channelId}:${selection.messageId}`;
    case "dm":
      return `dm:${selection.channelId}:${selection.messageId ?? ""}:${selection.threadRootId ?? ""}`;
  }
}

export function activityItemMatchesSelection(
  item: UserActivityItem,
  selection: ActivitySelection | null,
): boolean {
  if (!selection) return false;
  if (selection.kind === "inbox") {
    if (item.kind !== "inbox" || !item.inbox) return false;
    return (item.inbox.issue_id ?? item.inbox.id) === selection.key;
  }
  if (item.kind !== "thread") return false;
  const channelId = item.channel_id;
  const rootId = item.thread_root_message_id ?? item.id;
  if (!channelId || !rootId) return false;

  if (selection.kind === "dm") {
    return item.channel_kind === "dm" && channelId === selection.channelId;
  }
  if (selection.kind === "thread") {
    return (
      channelId === selection.channelId && rootId === selection.threadRootId
    );
  }
  if (selection.kind === "channel") {
    return channelId === selection.channelId && rootId === selection.messageId;
  }
  return false;
}
