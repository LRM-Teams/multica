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

// Lightweight ActorAvatar stub — the profile-click wiring (panel open, row
// opt-in attribute handling) is covered by common/actor-avatar tests; here we
// only assert which actor the row resolves and renders.
const avatarSpy = vi.fn();
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: (props: { actorType: string; actorId: string; size?: number }) => {
    avatarSpy(props);
    return (
      <span
        data-testid="row-avatar"
        data-actor-type={props.actorType}
        data-actor-id={props.actorId}
      />
    );
  },
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

  // LRM-809: the row renders the actor avatar (agent dm peer / root author /
  // inbox actor) and opts the row into avatar profile clicks.
  it("renders the agent actor avatar and opts the row into profile entry", () => {
    avatarSpy.mockClear();
    render(
      <ActivityListRow
        item={{ ...baseThread, actor_type: "agent", actor_id: "agent-9" }}
        isSelected={false}
        onClick={() => {}}
      />,
    );

    const avatar = screen.getByTestId("row-avatar");
    expect(avatar.dataset.actorType).toBe("agent");
    expect(avatar.dataset.actorId).toBe("agent-9");
    expect(
      screen.getByTestId("activity-row-thread-root-1").dataset.avatarProfileEntry,
    ).toBe("true");
  });

  it("maps backend 'user' actor_type onto the member directory type", () => {
    render(
      <ActivityListRow
        item={{ ...baseThread, actor_type: "user", actor_id: "user-7" }}
        isSelected={false}
        onClick={() => {}}
      />,
    );

    const avatar = screen.getByTestId("row-avatar");
    expect(avatar.dataset.actorType).toBe("member");
    expect(avatar.dataset.actorId).toBe("user-7");
  });

  it("falls back to the embedded inbox actor for older payloads", () => {
    render(
      <ActivityListRow
        item={{
          ...baseThread,
          kind: "inbox",
          id: "inbox-1",
          inbox: {
            actor_type: "agent",
            actor_id: "agent-2",
          } as never,
        }}
        isSelected={false}
        onClick={() => {}}
      />,
    );

    const avatar = screen.getByTestId("row-avatar");
    expect(avatar.dataset.actorType).toBe("agent");
    expect(avatar.dataset.actorId).toBe("agent-2");
  });

  it("keeps the kind icon when no actor is available (deploy skew / unknown)", () => {
    render(
      <ActivityListRow
        item={baseThread}
        isSelected={false}
        onClick={() => {}}
      />,
    );

    expect(screen.queryByTestId("row-avatar")).not.toBeInTheDocument();
    expect(
      screen.getByTestId("activity-row-thread-root-1").dataset.avatarProfileEntry,
    ).toBeUndefined();
  });

  it("renders a non-interactive system glyph for system actors (no profile)", () => {
    render(
      <ActivityListRow
        item={{ ...baseThread, actor_type: "system", actor_id: "sys-1" }}
        isSelected={false}
        onClick={() => {}}
      />,
    );

    const avatar = screen.getByTestId("row-avatar");
    expect(avatar.dataset.actorType).toBe("system");
    // system rows still get the opt-in attribute — ActorAvatar itself renders
    // no profile trigger for non-agent/member types, so clicks stay inert.
  });
});
