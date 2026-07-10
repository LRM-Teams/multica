import type { ReactNode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { parseMemberSystemEvent } from "./channel-system-event";
import { MemberSystemEventContent } from "./channel-system-event-content";

const mockAgents = [{ id: "agent-9", handle: "nova" }];
const mockMembers = [
  { user_id: "user-1", handle: "frank" },
  { user_id: "user-2", handle: "wendy" },
];

const openPanelMock = vi.fn<(id: string) => void>();

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["agents"] }),
  memberListOptions: () => ({ queryKey: ["members"] }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: string[] }) => ({
    data: opts.queryKey[0] === "agents" ? mockAgents : mockMembers,
  }),
}));

vi.mock("@multica/core/identity", () => ({
  // Mirrors the real resolver: prefer the actor's handle, fall back to the
  // backend-supplied name.
  resolveActorHandle: (actor: { handle?: string } | undefined, fallback?: string) =>
    actor?.handle ?? fallback ?? "",
}));

vi.mock("../../common/agent-panel-context", () => ({
  // No local provider in this unit → panel-open falls through to the store.
  useOpenAgentPanel: () => null,
}));

vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (selector: (s: { open: (id: string) => void }) => unknown) =>
    selector({ open: openPanelMock }),
}));

vi.mock("../../common/actor-profile-popover", () => ({
  ActorProfileTrigger: ({
    memberType,
    memberId,
    children,
    onClickCapture,
  }: {
    memberType: string;
    memberId: string;
    children: ReactNode;
    onClickCapture?: (e: unknown) => void;
  }) => (
    <span
      data-testid="actor-token"
      data-member-type={memberType}
      data-member-id={memberId}
      onClickCapture={onClickCapture}
    >
      {children}
    </span>
  ),
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: unknown) => string) =>
      selector({
        message: {
          system_event: {
            member_added: "{target} was added to this channel by {actor}",
            member_removed: "{target} was removed from this channel by {actor}",
            member_left: "{target} left this channel",
          },
        },
      }),
  }),
}));

function systemMessage(part: unknown, overrides: Partial<ChannelMessage> = {}): ChannelMessage {
  return {
    type: "system",
    parts: part === undefined ? undefined : [{ type: "text", text: JSON.stringify(part) }],
    content: "fallback content",
    ...overrides,
  } as ChannelMessage;
}

describe("parseMemberSystemEvent", () => {
  it("parses a structured member-added event", () => {
    const event = parseMemberSystemEvent(
      systemMessage({
        event: "channel_member_added",
        params: { actor_id: "user-1", actor_name: "Frank", target_id: "user-2", target_name: "Wendy" },
      }),
    );
    expect(event).toEqual({
      event: "channel_member_added",
      actorId: "user-1",
      actorName: "Frank",
      targetId: "user-2",
      targetName: "Wendy",
    });
  });

  it("extracts the #456 fact-layer fields (type + canonical handle)", () => {
    const event = parseMemberSystemEvent(
      systemMessage({
        event: "channel_member_removed",
        params: {
          actor_id: "user-1",
          actor_type: "human",
          actor_handle: "frank",
          target_id: "agent-9",
          target_type: "agent",
          target_handle: "nova",
        },
      }),
    );
    expect(event).toMatchObject({
      actorType: "human",
      actorHandle: "frank",
      targetType: "agent",
      targetHandle: "nova",
    });
  });

  it("returns null for a non-system message", () => {
    expect(
      parseMemberSystemEvent(
        systemMessage({ event: "channel_member_added", params: { target_id: "user-2" } }, { type: "agent" }),
      ),
    ).toBeNull();
  });

  it("returns null when there is no structured part", () => {
    expect(parseMemberSystemEvent(systemMessage(undefined))).toBeNull();
  });

  it("ignores parts that are not a known member event", () => {
    expect(parseMemberSystemEvent(systemMessage({ event: "some_other_event", params: {} }))).toBeNull();
  });

  it("ignores an event missing a target id", () => {
    expect(
      parseMemberSystemEvent(systemMessage({ event: "channel_member_added", params: { actor_id: "user-1" } })),
    ).toBeNull();
  });
});

describe("MemberSystemEventContent", () => {
  beforeEach(() => openPanelMock.mockClear());

  it("composes target-first passive copy with clickable member tokens", () => {
    render(
      <MemberSystemEventContent
        event={{
          event: "channel_member_added",
          actorId: "user-1",
          targetId: "user-2",
          targetName: "Wendy",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@wendy was added to this channel by @frank");
    const tokens = screen.getAllByTestId("actor-token");
    expect(tokens).toHaveLength(2);
    expect(tokens[0]).toHaveAttribute("data-member-type", "user");
    expect(tokens[0]).toHaveAttribute("data-member-id", "user-2");
  });

  it("opens the agent panel when an agent target token is clicked", () => {
    render(
      <MemberSystemEventContent
        event={{ event: "channel_member_left", targetId: "agent-9" }}
      />,
    );
    expect(document.body.textContent).toBe("@nova left this channel");
    const token = screen.getByTestId("actor-token");
    expect(token).toHaveAttribute("data-member-type", "agent");
    fireEvent.click(token);
    expect(openPanelMock).toHaveBeenCalledWith("agent-9");
  });

  it("degrades an unresolved actor to plain, non-interactive text", () => {
    render(
      <MemberSystemEventContent
        event={{ event: "channel_member_left", targetId: "ghost-x", targetName: "Ghost" }}
      />,
    );
    expect(document.body.textContent).toBe("@Ghost left this channel");
    expect(screen.queryByTestId("actor-token")).toBeNull();
  });

  it("omits the actor slot for a member-left event", () => {
    render(
      <MemberSystemEventContent event={{ event: "channel_member_left", targetId: "user-1" }} />,
    );
    expect(document.body.textContent).toBe("@frank left this channel");
    expect(screen.getAllByTestId("actor-token")).toHaveLength(1);
  });

  it("uses the #456 fact layer so a removed member no longer in the cache stays clickable", () => {
    // ghost-x is NOT in mockAgents/mockMembers — the bridge path would degrade it
    // to plain text. With target_type/handle from the fact layer it stays a
    // clickable member token.
    render(
      <MemberSystemEventContent
        event={{
          event: "channel_member_left",
          targetId: "ghost-x",
          targetType: "human",
          targetHandle: "ghost",
          targetName: "Ghost",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@ghost left this channel");
    const token = screen.getByTestId("actor-token");
    expect(token).toHaveAttribute("data-member-type", "user");
    expect(token).toHaveAttribute("data-member-id", "ghost-x");
  });

  it("routes a fact-layer agent target to the side panel", () => {
    render(
      <MemberSystemEventContent
        event={{
          event: "channel_member_left",
          targetId: "agent-x",
          targetType: "agent",
          targetHandle: "atlas",
        }}
      />,
    );
    const token = screen.getByTestId("actor-token");
    expect(token).toHaveAttribute("data-member-type", "agent");
    fireEvent.click(token);
    expect(openPanelMock).toHaveBeenCalledWith("agent-x");
  });
});
