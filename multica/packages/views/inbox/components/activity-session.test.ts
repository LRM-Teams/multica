// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { UserActivityItem } from "@multica/core/types";
import {
  activityItemMatchesSelection,
  activitySelectionKey,
  activitySessionParams,
  activitySessionUrl,
  resolveActivitySessionSurface,
} from "./activity-session";

function thread(
  overrides: Partial<UserActivityItem> = {},
): UserActivityItem {
  return {
    kind: "thread",
    id: "root-1",
    workspace_id: "ws-1",
    channel_id: "ch-1",
    channel_name: "general",
    channel_kind: "channel",
    updated_at: new Date().toISOString(),
    unread_count: 0,
    preview_text: "hi",
    title: "#general: hi",
    access_denied: false,
    thread_root_message_id: "root-1",
    reply_count: 0,
    followed: false,
    participated: false,
    ...overrides,
  };
}

describe("resolveActivitySessionSurface", () => {
  it("maps DM threads to dm surface", () => {
    expect(
      resolveActivitySessionSurface(
        thread({ channel_kind: "dm", channel_name: "Barry", reply_count: 3 }),
      ),
    ).toBe("dm");
  });

  it("maps group threads with replies to thread surface", () => {
    expect(resolveActivitySessionSurface(thread({ reply_count: 2 }))).toBe(
      "thread",
    );
  });

  it("maps followed/participated zero-reply roots to thread surface", () => {
    expect(
      resolveActivitySessionSurface(thread({ followed: true, reply_count: 0 })),
    ).toBe("thread");
    expect(
      resolveActivitySessionSurface(
        thread({ participated: true, reply_count: 0 }),
      ),
    ).toBe("thread");
  });

  it("maps bare channel mentions to full channel stream surface", () => {
    expect(resolveActivitySessionSurface(thread({ reply_count: 0 }))).toBe(
      "channel",
    );
  });

  it("returns null for inbox rows", () => {
    expect(
      resolveActivitySessionSurface({
        kind: "inbox",
        channel_kind: "channel",
        reply_count: 1,
        followed: false,
        participated: false,
      }),
    ).toBeNull();
  });
});

describe("activitySessionParams", () => {
  it("uses thread= for thread surface and message= for channel surface", () => {
    expect(activitySessionParams(thread({ reply_count: 4 }), "unread")).toEqual(
      {
        tab: "unread",
        channel: "ch-1",
        thread: "root-1",
      },
    );
    expect(activitySessionParams(thread({ reply_count: 0 }), "all")).toEqual({
      channel: "ch-1",
      message: "root-1",
    });
  });

  it("keeps issue= for inbox rows", () => {
    const item: UserActivityItem = {
      kind: "inbox",
      id: "act-1",
      workspace_id: "ws-1",
      updated_at: new Date().toISOString(),
      unread_count: 1,
      preview_text: "",
      title: "Issue",
      access_denied: false,
      inbox: {
        id: "inbox-1",
        workspace_id: "ws-1",
        recipient_type: "member",
        recipient_id: "u-1",
        actor_type: "member",
        actor_id: "u-2",
        type: "mentioned",
        severity: "attention",
        title: "Issue",
        body: null,
        issue_status: null,
        read: false,
        archived: false,
        created_at: new Date().toISOString(),
        issue_id: "issue-9",
        details: null,
      },
    };
    expect(activitySessionParams(item)).toEqual({ issue: "issue-9" });
  });
});

describe("activitySessionUrl + selection key", () => {
  it("encodes channel+thread without issue", () => {
    expect(
      activitySessionUrl("/ws/inbox", {
        channel: "ch-1",
        thread: "root-1",
        tab: "mentions",
      }),
    ).toBe("/ws/inbox?tab=mentions&channel=ch-1&thread=root-1");
  });

  it("uses thread root id as selection key", () => {
    expect(activitySelectionKey(thread())).toBe("root-1");
  });

  it("matches thread selection by id or thread_root_message_id", () => {
    const item = thread({
      id: "root-1",
      thread_root_message_id: "root-1",
    });
    expect(activityItemMatchesSelection(item, "root-1")).toBe(true);
    expect(activityItemMatchesSelection(item, "other")).toBe(false);
  });
});
