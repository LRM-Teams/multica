import { describe, expect, it } from "vitest";
import type { UserActivityItem } from "@multica/core/types";
import {
  activityItemMatchesSelection,
  inboxActivityUrl,
  parseActivitySelection,
  resolveActivityPaneKind,
  selectionFromActivityItem,
} from "./activity-selection";

function threadItem(
  overrides: Partial<UserActivityItem> &
    Pick<UserActivityItem, "id" | "channel_id">,
): UserActivityItem {
  return {
    kind: "thread",
    workspace_id: "ws1",
    updated_at: "2026-07-23T00:00:00Z",
    unread_count: 0,
    preview_text: "hello",
    title: "#eng: hello",
    access_denied: false,
    channel_kind: "group",
    channel_name: "eng",
    thread_root_message_id: overrides.id,
    reply_count: 0,
    ...overrides,
  };
}

describe("resolveActivityPaneKind", () => {
  it("maps inbox rows to inbox pane", () => {
    const item: UserActivityItem = {
      kind: "inbox",
      id: "in1",
      workspace_id: "ws1",
      updated_at: "2026-07-23T00:00:00Z",
      unread_count: 1,
      preview_text: "",
      title: "Assigned",
      access_denied: false,
      inbox: {
        id: "in1",
        workspace_id: "ws1",
        recipient_type: "member",
        recipient_id: "u1",
        actor_type: "member",
        actor_id: "u2",
        type: "issue_assigned",
        severity: "action_required",
        title: "Assigned",
        body: "",
        issue_status: null,
        read: false,
        archived: false,
        created_at: "2026-07-23T00:00:00Z",
        issue_id: "iss1",
        details: null,
      },
    };
    expect(resolveActivityPaneKind(item)).toBe("inbox");
  });

  it("maps DM threads to dm pane", () => {
    expect(
      resolveActivityPaneKind(
        threadItem({
          id: "m1",
          channel_id: "dm1",
          channel_kind: "dm",
          reply_count: 3,
        }),
      ),
    ).toBe("dm");
  });

  it("maps group threads with replies to thread pane", () => {
    expect(
      resolveActivityPaneKind(
        threadItem({ id: "m1", channel_id: "c1", reply_count: 2 }),
      ),
    ).toBe("thread");
  });

  it("maps group top-level mentions (no replies) to channel stream", () => {
    expect(
      resolveActivityPaneKind(
        threadItem({ id: "m1", channel_id: "c1", reply_count: 0 }),
      ),
    ).toBe("channel");
  });
});

describe("parseActivitySelection / inboxActivityUrl", () => {
  it("round-trips thread selection", () => {
    const url = inboxActivityUrl("/acme/inbox", {
      tab: "unread",
      selection: {
        kind: "thread",
        channelId: "c1",
        threadRootId: "r1",
      },
    });
    expect(url).toBe("/acme/inbox?tab=unread&channel=c1&thread=r1");
    expect(parseActivitySelection(new URLSearchParams(url.split("?")[1]))).toEqual({
      kind: "thread",
      channelId: "c1",
      threadRootId: "r1",
    });
  });

  it("round-trips channel stream selection", () => {
    const url = inboxActivityUrl("/acme/inbox", {
      selection: { kind: "channel", channelId: "c1", messageId: "m1" },
    });
    expect(url).toBe("/acme/inbox?channel=c1&message=m1");
    expect(parseActivitySelection(new URLSearchParams("channel=c1&message=m1"))).toEqual({
      kind: "channel",
      channelId: "c1",
      messageId: "m1",
    });
  });

  it("round-trips dm selection", () => {
    const url = inboxActivityUrl("/acme/inbox", {
      selection: { kind: "dm", channelId: "dm1", messageId: "m1" },
    });
    expect(url).toBe("/acme/inbox?dm=dm1&message=m1");
    expect(parseActivitySelection(new URLSearchParams("dm=dm1&message=m1"))).toEqual({
      kind: "dm",
      channelId: "dm1",
      messageId: "m1",
    });
  });

  it("prefers issue over conversation params", () => {
    expect(
      parseActivitySelection(
        new URLSearchParams("issue=iss1&channel=c1&thread=r1"),
      ),
    ).toEqual({ kind: "inbox", key: "iss1" });
  });
});

describe("selectionFromActivityItem / activityItemMatchesSelection", () => {
  it("builds and matches a thread selection", () => {
    const item = threadItem({ id: "m1", channel_id: "c1", reply_count: 4 });
    const selection = selectionFromActivityItem(item);
    expect(selection).toEqual({
      kind: "thread",
      channelId: "c1",
      threadRootId: "m1",
    });
    expect(activityItemMatchesSelection(item, selection)).toBe(true);
  });

  it("rejects access-denied rows", () => {
    expect(
      selectionFromActivityItem(
        threadItem({
          id: "m1",
          channel_id: "c1",
          access_denied: true,
          reply_count: 1,
        }),
      ),
    ).toBeNull();
  });
});
