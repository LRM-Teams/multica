import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { UserActivityItem } from "@multica/core/types";
import { ActivityListRow } from "./activity-list-row";

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (selector: (bundle: Record<string, unknown>) => string, vars?: { count?: number }) => {
      const key = selector({
        activity: {
          new_count: "new_count",
          replies: "replies",
          access_denied: "No access",
        },
      });
      if (key === "new_count") return `${vars?.count ?? 0} new`;
      if (key === "replies") return `${vars?.count ?? 0} replies`;
      return key;
    },
  }),
  Time: ({ value }: { kind: string; value: string }) => <span>{value}</span>,
}));

const baseThread: UserActivityItem = {
  kind: "thread",
  id: "root-1",
  workspace_id: "ws-1",
  channel_id: "ch-1",
  channel_name: "general",
  channel_kind: "channel",
  updated_at: new Date().toISOString(),
  unread_count: 2,
  preview_text: "Hello thread",
  title: "Thread in #general",
  access_denied: false,
  thread_root_message_id: "root-1",
  reply_count: 3,
};

describe("ActivityListRow", () => {
  it("renders unread thread row with badge and replies", () => {
    render(
      <ActivityListRow
        item={baseThread}
        isSelected={false}
        onClick={() => {}}
      />,
    );

    expect(screen.getByText("Thread in #general")).toBeInTheDocument();
    expect(screen.getByText("2 new")).toBeInTheDocument();
    expect(screen.getByText("3 replies")).toBeInTheDocument();
    expect(screen.getByText("#general")).toBeInTheDocument();
  });

  it("hides unread chrome when unread_count is 0 (LRM-379)", () => {
    render(
      <ActivityListRow
        item={{ ...baseThread, unread_count: 0 }}
        isSelected={false}
        onClick={() => {}}
      />,
    );

    expect(screen.queryByText(/new$/)).not.toBeInTheDocument();
    expect(screen.getByText("3 replies")).toBeInTheDocument();
  });
});
