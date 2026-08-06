import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { UserActivityItem, UserActivityListResponse } from "../types";
import { userActivityKeys } from "./queries";
import {
  markActivityItemRead,
  optimisticallyMarkActivityInboxRead,
  optimisticallyMarkActivityThreadRead,
  restoreActivityQueries,
} from "./mutations";

const wsId = "ws-1";

function threadItem(overrides: Partial<UserActivityItem> = {}): UserActivityItem {
  return {
    kind: "thread",
    id: "root-1",
    workspace_id: wsId,
    channel_id: "ch-1",
    updated_at: "2026-07-23T00:00:00Z",
    unread_count: 3,
    preview_text: "hello",
    title: "#chan: hello",
    access_denied: false,
    thread_root_message_id: "root-1",
    reply_count: 9,
    ...overrides,
  };
}

function inboxItem(overrides: Partial<UserActivityItem> = {}): UserActivityItem {
  return {
    kind: "inbox",
    id: "inbox-1",
    workspace_id: wsId,
    updated_at: "2026-07-23T00:00:00Z",
    unread_count: 1,
    preview_text: "assigned",
    title: "Issue title",
    access_denied: false,
    inbox: {
      id: "inbox-1",
      workspace_id: wsId,
      recipient_type: "member",
      recipient_id: "u-1",
      actor_type: "member",
      actor_id: "u-2",
      type: "issue_assigned",
      severity: "info",
      title: "Issue title",
      body: "assigned",
      issue_status: null,
      read: false,
      archived: false,
      created_at: "2026-07-23T00:00:00Z",
      issue_id: "issue-1",
      details: null,
    },
    ...overrides,
  };
}

describe("markActivityItemRead", () => {
  it("clears thread unread_count", () => {
    expect(markActivityItemRead(threadItem()).unread_count).toBe(0);
  });

  it("clears inbox unread and flips inbox.read", () => {
    const next = markActivityItemRead(inboxItem());
    expect(next.unread_count).toBe(0);
    expect(next.inbox?.read).toBe(true);
  });

  it("is a no-op when already read", () => {
    const item = threadItem({ unread_count: 0 });
    expect(markActivityItemRead(item)).toBe(item);
  });
});

describe("optimisticallyMarkActivityThreadRead", () => {
  it("zeros matching thread rows across activity tabs and restores on rollback", async () => {
    const qc = new QueryClient();
    const unread: UserActivityListResponse = {
      items: [threadItem(), threadItem({ id: "root-2", thread_root_message_id: "root-2", unread_count: 1 })],
    };
    const all: UserActivityListResponse = { items: [...unread.items] };
    qc.setQueryData(userActivityKeys.list(wsId, "unread"), unread);
    qc.setQueryData(userActivityKeys.list(wsId, "all"), all);

    const prev = await optimisticallyMarkActivityThreadRead(qc, wsId, "root-1");

    const unreadAfter = qc.getQueryData<UserActivityListResponse>(
      userActivityKeys.list(wsId, "unread"),
    );
    // Unread tab drops the cleared row immediately.
    expect(unreadAfter?.items).toHaveLength(1);
    expect(unreadAfter?.items[0]?.id).toBe("root-2");

    const allAfter = qc.getQueryData<UserActivityListResponse>(
      userActivityKeys.list(wsId, "all"),
    );
    expect(allAfter?.items[0]?.unread_count).toBe(0);
    expect(allAfter?.items[1]?.unread_count).toBe(1);

    restoreActivityQueries(qc, prev);
    expect(
      qc.getQueryData<UserActivityListResponse>(userActivityKeys.list(wsId, "unread"))
        ?.items,
    ).toHaveLength(2);
  });
});

describe("optimisticallyMarkActivityInboxRead", () => {
  it("zeros matching inbox rows", async () => {
    const qc = new QueryClient();
    qc.setQueryData(userActivityKeys.list(wsId, "all"), {
      items: [inboxItem(), threadItem()],
    } satisfies UserActivityListResponse);

    await optimisticallyMarkActivityInboxRead(qc, wsId, "inbox-1");

    const after = qc.getQueryData<UserActivityListResponse>(
      userActivityKeys.list(wsId, "all"),
    );
    expect(after?.items[0]?.unread_count).toBe(0);
    expect(after?.items[0]?.inbox?.read).toBe(true);
    expect(after?.items[1]?.unread_count).toBe(3);
  });
});
