import type { ReactNode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelMessage, MessagePart } from "@multica/core/types";
import {
  parseMemberSystemEvent,
  parseIssueSystemEvent,
  parseIssueAggregateSystemEvent,
  parseProjectSystemEvent,
  parseThreadSystemEvent,
  foldedIssueEventIds,
  type IssueSystemEvent,
  type ProjectSystemEvent,
  type ThreadSystemEvent,
} from "./channel-system-event";
import {
  MemberSystemEventContent,
  IssueSystemEventContent,
  IssueAggregateSystemEventContent,
  ProjectSystemEventContent,
  ThreadSystemEventContent,
} from "./channel-system-event-content";

const mockAgents = [
  { id: "agent-9", handle: "nova", display_name: "nova" },
  { id: "agent-be", handle: "hou-duan", display_name: "后端工程师" },
  { id: "agent-fe", handle: "qian-duan", display_name: "前端工程师" },
];
const mockMembers = [
  { user_id: "user-1", handle: "frank", display_name: "frank" },
  { user_id: "user-2", handle: "wendy", display_name: "wendy" },
];
const mockProfiles: Record<string, { display_name: string; name: string; member_type: string; member_id: string }> = {
  "agent-beckham": {
    member_type: "agent",
    member_id: "agent-beckham",
    name: "bei-ke-han-mu-11",
    display_name: "贝克汉姆",
  },
};

const openPanelMock = vi.fn<(id: string) => void>();

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["agents"] }),
  memberListOptions: () => ({ queryKey: ["members"] }),
  memberProfileOptions: (_wsId: string, type: string, id: string) => ({
    queryKey: ["workspaces", "ws-1", "member-profiles", type, id],
    enabled: !!id,
  }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: readonly unknown[] }) => {
    const key = opts.queryKey as string[];
    // workspaceKeys.* → ["workspaces", wsId, "agents"|"members"|...]
    if (key[0] === "agents" || key[2] === "agents") {
      return { data: mockAgents, isPending: false, isError: false };
    }
    if (key[0] === "members" || key[2] === "members") {
      return { data: mockMembers, isPending: false, isError: false };
    }
    if (key[2] === "member-profiles") {
      const id = key[4];
      if (!id) return { data: undefined, isPending: false, isError: false };
      const profile = mockProfiles[id];
      return { data: profile, isPending: false, isError: !profile };
    }
    return { data: undefined, isPending: false, isError: false };
  },
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
  useAgentXpBurstStore: (selector: (s: { bursts: Record<string, never> }) => unknown) =>
    selector({ bursts: {} }),
}));

// System rows render actors through the ordinary @mention component (#603),
// not the bespoke ActorProfileTrigger. Stub it with the same "actor-token"
// shape the pre-#603 tests already assert against, and replicate the
// agent-side-panel-on-click affordance via the already-mocked panel store so
// that behavior stays covered without depending on ActorMention's real
// internals (which need a live auth store this unit doesn't provide).
vi.mock("../../common/markdown", () => ({
  ActorMention: ({
    type,
    id,
    label,
  }: {
    type: string;
    id: string;
    label?: string;
  }) => (
    <span
      data-testid="actor-token"
      data-member-type={type}
      data-member-id={id}
      onClick={type === "agent" ? () => openPanelMock(id) : undefined}
    >
      {label}
    </span>
  ),
}));

// The issue-ref token is the ONLY interactive element in an issue row — stub it
// to a bare link so these tests can assert "exactly one link, and it's the ref".
vi.mock("../../issues/components/issue-ref-link", () => ({
  IssueRefLink: ({ issueId, text }: { issueId: string; text: string }) => (
    <a href={`/issues/${issueId}`} data-issue-ref="">
      {text}
    </a>
  ),
}));

// Project rows (#610) link the project name to the workspace project route.
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    projectDetail: (id: string) => `/ws/projects/${id}`,
  }),
}));
vi.mock("../../navigation/app-link", () => ({
  AppLink: ({
    href,
    children,
    className,
  }: {
    href: string;
    children: ReactNode;
    className?: string;
  }) => (
    <a href={href} className={className} data-project-ref="">
      {children}
    </a>
  ),
}));
vi.mock("../../navigation/context", () => ({
  useOptionalNavigation: () => ({ pathname: "/ws/channels", searchParams: new URLSearchParams() }),
}));

// Issue rows resolve the actor/assignee display name from the identity cache
// or member-profile API (LRM-281) — never emit-time name fallbacks.
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (type: string, id: string, fallback?: string) => {
      // Respect type the same way production getActorName does — a member probe
      // must not resolve an agent id (and vice versa), or untyped rows mis-tag.
      if (type === "agent") {
        const agent = mockAgents.find((a) => a.id === id);
        if (agent) return agent.display_name || agent.handle;
        return fallback ?? "Unknown Agent";
      }
      if (type === "member") {
        const member = mockMembers.find((m) => m.user_id === id);
        if (member) return member.display_name || member.handle;
        return fallback ?? "Unknown";
      }
      return fallback ?? "Unknown";
    },
  }),
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: unknown) => string, options?: Record<string, unknown>) => {
      const raw = selector({
        message: {
          system_event: {
            member_added: "{target} was added to this channel by {actor}",
            member_added_no_actor: "{target} joined this channel",
            member_removed: "{target} was removed from this channel by {actor}",
            member_removed_no_actor: "{target} was removed from this channel",
            member_left: "{target} left this channel",
            issue: {
              actor_system: "Multica",
              created: "{actor} 创建了 {issue}",
              assigned: "{actor} 将 {issue} 指派给 {target}",
              assigned_unknown: "{actor} 重新指派了 {issue}",
              in_progress: "{actor} 开始了 {issue}",
              in_review: "{actor} 将 {issue} 移至「评审」",
              done: "{actor} 完成了 {issue}",
              updated: "{actor} 更新了 {issue}",
              status: "{actor} 将 {issue} 移至「{{status}}」",
              aggregate_created: "{actor} 创建了 {issues}",
              aggregate_done: "{actor} 完成了 {issues}",
              aggregate_assigned: "{actor} 指派了 {issues}",
              aggregate_started: "{actor} 开始了 {issues}",
              aggregate_in_review: "{actor} 将 {issues} 移至「评审」",
              aggregate_updated: "{actor} 更新了 {issues}",
              aggregate_expand: "展开更多事项",
              aggregate_collapse: "收起事项列表",
            },
            issue_status: {
              backlog: "待办事项",
              todo: "待办",
              in_progress: "处理中",
              in_review: "待审",
              done: "已完成",
              blocked: "已阻塞",
              cancelled: "已取消",
            },
            project: {
              actor_system: "Multica",
              bound: "{actor} 把本群关联到项目「{project}」",
              changed: "{actor} 把关联项目从「{previous}」改为「{project}」",
              unbound: "{actor} 解除了与项目「{previous}」的关联",
            },
            thread: {
              unfollowed: "{actor} 取消关注了此话题",
              followed: "{actor} 关注了此话题",
            },
          },
        },
      });
      // Mirror i18next's `{{ }}` interpolation; the single-brace `{issue}` slot is
      // left for the component to split out into the React token.
      return options
        ? raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(options[key] ?? ""))
        : raw;
    },
  }),
}));

function systemMessage(
  part: { event: string; params?: Record<string, unknown> } | undefined,
  overrides: Partial<ChannelMessage> = {},
): ChannelMessage {
  return {
    type: "system",
    parts:
      part === undefined
        ? undefined
        : [
            {
              type: "system_event",
              event: part.event,
              event_params: part.params ?? {},
            },
          ],
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

  it("does not retain a text-JSON compatibility reader after migration", () => {
    const legacyPart: MessagePart = {
      type: "text",
      text: JSON.stringify({
        event: "channel_member_added",
        params: { target_id: "user-2" },
      }),
    };
    expect(
      parseMemberSystemEvent({
        type: "system",
        parts: [legacyPart],
        content: "fallback content",
      } as ChannelMessage),
    ).toBeNull();
  });

  it("ignores parts that are not a known member event", () => {
    expect(parseMemberSystemEvent(systemMessage({ event: "some_other_event", params: {} }))).toBeNull();
  });

  it("ignores an event missing a target id", () => {
    expect(
      parseMemberSystemEvent(systemMessage({ event: "channel_member_added", params: { actor_id: "user-1" } })),
    ).toBeNull();
  });

  it("extracts `source` for an actor-less system-maintained row (#661)", () => {
    const event = parseMemberSystemEvent(
      systemMessage({
        event: "channel_member_added",
        params: { target_id: "user-2", source: "system_invariant" },
      }),
    );
    expect(event).toMatchObject({ source: "system_invariant", actorId: undefined });
  });

  it("leaves `source` undefined for an older row that predates the field", () => {
    const event = parseMemberSystemEvent(
      systemMessage({
        event: "channel_member_added",
        params: { actor_id: "user-1", target_id: "user-2" },
      }),
    );
    expect(event).toMatchObject({ source: undefined });
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
    expect(tokens[0]).toHaveAttribute("data-member-type", "member");
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
    // No type + not in directory/profile → honest id label, never emit-time name (LRM-281).
    render(
      <MemberSystemEventContent
        event={{ event: "channel_member_left", targetId: "ghost-x", targetName: "Ghost" }}
      />,
    );
    expect(document.body.textContent).toBe("@ghost-x left this channel");
    expect(screen.queryByTestId("actor-token")).toBeNull();
  });

  it("omits the actor slot for a member-left event", () => {
    render(
      <MemberSystemEventContent event={{ event: "channel_member_left", targetId: "user-1" }} />,
    );
    expect(document.body.textContent).toBe("@frank left this channel");
    expect(screen.getAllByTestId("actor-token")).toHaveLength(1);
  });

  it("keeps typed fact-layer members clickable when directory/profile miss (no name fallback)", () => {
    // ghost-x is NOT in mockAgents/mockMembers/profiles. Typed fact keeps the
    // mention clickable; display uses the id until profile resolves (LRM-281).
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
    expect(document.body.textContent).toBe("@ghost-x left this channel");
    expect(document.body.textContent).not.toContain("Ghost");
    const token = screen.getByTestId("actor-token");
    expect(token).toHaveAttribute("data-member-type", "member");
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

  it("drops the dangling 'by' clause for an actor-less system-maintained add (#661)", () => {
    render(
      <MemberSystemEventContent
        event={{
          event: "channel_member_added",
          targetId: "user-2",
          source: "system_invariant",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@wendy joined this channel");
    expect(screen.getAllByTestId("actor-token")).toHaveLength(1);
  });

  it("drops the dangling 'by' clause for an actor-less removal, and never says 'left' (#661)", () => {
    render(
      <MemberSystemEventContent
        event={{
          event: "channel_member_removed",
          targetId: "user-2",
          source: "system_invariant",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@wendy was removed from this channel");
    expect(document.body.textContent).not.toContain("left");
  });

  it("still uses the manual template when a real actor is present, even without `source` (old rows)", () => {
    render(
      <MemberSystemEventContent
        event={{
          event: "channel_member_added",
          actorId: "user-1",
          targetId: "user-2",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@wendy was added to this channel by @frank");
  });
});

function issueMsg(
  id: string,
  event: string,
  params: Record<string, unknown>,
  overrides: Partial<ChannelMessage> = {},
): ChannelMessage {
  return systemMessage({ event, params }, { id, ...overrides });
}

describe("parseIssueSystemEvent", () => {
  it("projects the load-bearing facts from a status-change part", () => {
    const event = parseIssueSystemEvent(
      issueMsg("m1", "issue_status_changed", {
        issue_id: "issue-uuid",
        issue_identifier: "LRM-137",
        issue_status: "in_progress",
        previous_status: "todo",
        actor_id: "agent-be",
        actor_type: "agent",
        actor_handle: "bei-duan",
        actor_name: "后端工程师",
      }),
    );
    expect(event).toMatchObject({
      event: "issue_status_changed",
      issueId: "issue-uuid",
      issueIdentifier: "LRM-137",
      issueStatus: "in_progress",
      previousStatus: "todo",
      actorId: "agent-be",
      actorType: "agent",
      actorHandle: "bei-duan",
      actorName: "后端工程师",
    });
  });

  it("returns null when a load-bearing fact is missing (falls back to raw content)", () => {
    expect(
      parseIssueSystemEvent(
        issueMsg("m1", "issue_status_changed", {
          issue_identifier: "LRM-137",
          issue_status: "in_progress",
        }),
      ),
    ).toBeNull();
  });

  it("ignores non-issue system events", () => {
    expect(
      parseIssueSystemEvent(issueMsg("m1", "channel_member_added", { target_id: "user-2" })),
    ).toBeNull();
  });

  it("parses issue_created without a status — fixed verb, no status fact (#610)", () => {
    const event = parseIssueSystemEvent(
      issueMsg("m1", "issue_created", {
        issue_id: "issue-uuid",
        issue_identifier: "LRM-200",
        actor_id: "agent-be",
        actor_type: "agent",
      }),
    );
    expect(event).toMatchObject({
      event: "issue_created",
      issueId: "issue-uuid",
      issueIdentifier: "LRM-200",
      actorId: "agent-be",
    });
    // No status is emitted for a creation, and the parser must not invent one.
    expect(event?.issueStatus).toBeUndefined();
  });

  it("still requires id + identifier for issue_created", () => {
    expect(
      parseIssueSystemEvent(issueMsg("m1", "issue_created", { issue_id: "issue-uuid" })),
    ).toBeNull();
  });

  it("skips rows that carry an items[] aggregate (owned by parseIssueAggregateSystemEvent)", () => {
    expect(
      parseIssueSystemEvent(
        issueMsg("m1", "issue_completed", {
          actor_id: "agent-fe",
          actor_type: "agent",
          items: [
            { issue_id: "i1", issue_identifier: "LRM-360", issue_status: "done" },
            { issue_id: "i2", issue_identifier: "LRM-357", issue_status: "done" },
          ],
        }),
      ),
    ).toBeNull();
  });
});

describe("parseIssueAggregateSystemEvent", () => {
  it("parses a server-authored multi-issue completed aggregate", () => {
    const event = parseIssueAggregateSystemEvent(
      issueMsg("m1", "issue_completed", {
        actor_id: "agent-fe",
        actor_type: "agent",
        items: [
          {
            issue_id: "i1",
            issue_identifier: "LRM-360",
            issue_title: "Attachment contrast",
            issue_status: "done",
          },
          {
            issue_id: "i2",
            issue_identifier: "LRM-357",
            issue_title: "Empty tokens",
            issue_status: "done",
          },
        ],
      }),
    );
    expect(event).toMatchObject({
      event: "issue_completed",
      actorId: "agent-fe",
      actorType: "agent",
      items: [
        {
          issueId: "i1",
          issueIdentifier: "LRM-360",
          issueTitle: "Attachment contrast",
          issueStatus: "done",
        },
        {
          issueId: "i2",
          issueIdentifier: "LRM-357",
          issueTitle: "Empty tokens",
          issueStatus: "done",
        },
      ],
    });
  });

  it("returns null when items is missing — no client-side fake aggregation", () => {
    expect(
      parseIssueAggregateSystemEvent(
        issueMsg("m1", "issue_completed", {
          issue_id: "i1",
          issue_identifier: "LRM-360",
          issue_status: "done",
          actor_id: "agent-fe",
          actor_type: "agent",
        }),
      ),
    ).toBeNull();
  });

  it("returns null when any item is missing issue_id/identifier (refuse whole group)", () => {
    expect(
      parseIssueAggregateSystemEvent(
        issueMsg("m1", "issue_completed", {
          actor_id: "agent-fe",
          actor_type: "agent",
          items: [
            { issue_id: "i1", issue_identifier: "LRM-360", issue_status: "done" },
            { issue_identifier: "LRM-357", issue_status: "done" },
          ],
        }),
      ),
    ).toBeNull();
  });

  it("returns null for an empty items array", () => {
    expect(
      parseIssueAggregateSystemEvent(
        issueMsg("m1", "issue_completed", {
          actor_id: "agent-fe",
          actor_type: "agent",
          items: [],
        }),
      ),
    ).toBeNull();
  });

  it("orders items by occurred_at then issue_id (LRM-422 stamp)", () => {
    const event = parseIssueAggregateSystemEvent(
      issueMsg("m1", "issue_completed", {
        actor_id: "agent-fe",
        actor_type: "agent",
        items: [
          {
            issue_id: "i2",
            issue_identifier: "LRM-357",
            issue_status: "done",
            occurred_at: "2026-07-23T08:02:00Z",
          },
          {
            issue_id: "i1",
            issue_identifier: "LRM-360",
            issue_status: "done",
            occurred_at: "2026-07-23T08:00:00Z",
          },
          {
            issue_id: "i3",
            issue_identifier: "LRM-361",
            issue_status: "done",
            occurred_at: "2026-07-23T08:01:00Z",
          },
        ],
      }),
    );
    expect(event?.items.map((item) => item.issueIdentifier)).toEqual([
      "LRM-360",
      "LRM-361",
      "LRM-357",
    ]);
  });
});

describe("IssueAggregateSystemEventContent", () => {
  it("inlines brand chips for N=2–3 without a fold control (LRM-423 / LRM-564)", () => {
    render(
      <IssueAggregateSystemEventContent
        event={{
          event: "issue_completed",
          actorId: "agent-fe",
          actorType: "agent",
          items: [
            {
              issueId: "i1",
              issueIdentifier: "LRM-360",
              issueTitle: "Fix contrast",
              issueStatus: "done",
            },
            {
              issueId: "i2",
              issueIdentifier: "LRM-357",
              issueTitle: "Empty tokens",
              issueStatus: "done",
            },
            {
              issueId: "i3",
              issueIdentifier: "LRM-353",
              issueTitle: "Composer density",
              issueStatus: "done",
            },
          ],
        }}
        sourceMessageId="msg-agg"
      />,
    );
    expect(document.body.textContent).toContain("@前端工程师 完成了");
    // LRM-609 SoT A': title-primary unfilled brand text (identifier in peek only).
    expect(document.body.textContent).toContain("Fix contrast");
    expect(document.body.textContent).toContain("Empty tokens");
    expect(document.body.textContent).toContain("Composer density");
    expect(document.body.textContent).not.toContain("LRM-360");
    expect(document.body.textContent).not.toMatch(/3 个 Issue/);
    expect(screen.queryByTestId("issue-aggregate-expand")).toBeNull();
  });

  it("folds N≥4 behind +N and expands remaining chips (LRM-423 / LRM-564)", () => {
    render(
      <IssueAggregateSystemEventContent
        event={{
          event: "issue_completed",
          actorId: "agent-fe",
          actorType: "agent",
          items: [
            { issueId: "i1", issueIdentifier: "LRM-360", issueTitle: "A", issueStatus: "done" },
            { issueId: "i2", issueIdentifier: "LRM-357", issueTitle: "B", issueStatus: "done" },
            { issueId: "i3", issueIdentifier: "LRM-353", issueTitle: "C", issueStatus: "done" },
            { issueId: "i4", issueIdentifier: "LRM-350", issueTitle: "D", issueStatus: "done" },
          ],
        }}
      />,
    );
    expect(document.body.textContent).toContain("A");
    expect(document.body.textContent).not.toContain("LRM-360");
    expect(document.body.textContent).not.toContain("B");
    expect(screen.getByTestId("issue-aggregate-expand").textContent).toContain("+3");
    fireEvent.click(screen.getByTestId("issue-aggregate-expand"));
    const list = screen.getByTestId("issue-aggregate-items");
    expect(list.textContent).toContain("B");
    expect(list.textContent).toContain("C");
    expect(list.textContent).toContain("D");
  });

  it("hides the expand control for a single-item aggregate", () => {
    render(
      <IssueAggregateSystemEventContent
        event={{
          event: "issue_completed",
          actorId: "agent-fe",
          actorType: "agent",
          items: [
            {
              issueId: "i1",
              issueIdentifier: "LRM-360",
              issueTitle: "Solo title",
              issueStatus: "done",
            },
          ],
        }}
      />,
    );
    expect(screen.queryByTestId("issue-aggregate-expand")).toBeNull();
    expect(document.body.textContent).toContain("@前端工程师 完成了");
    expect(document.body.textContent).toContain("Solo title");
    expect(document.body.textContent).not.toContain("LRM-360");
    expect(document.body.textContent).not.toMatch(/1 个 Issue/);
  });
});

describe("parseProjectSystemEvent", () => {
  it("parses a bind — current project only", () => {
    const event = parseProjectSystemEvent(
      issueMsg("m1", "channel_project_bound", {
        project_id: "proj-1",
        project_title: "Q3 Roadmap",
        actor_id: "agent-be",
        actor_type: "agent",
      }),
    );
    expect(event).toMatchObject({
      event: "channel_project_bound",
      projectId: "proj-1",
      projectTitle: "Q3 Roadmap",
      actorId: "agent-be",
    });
    expect(event?.previousProjectId).toBeUndefined();
  });

  it("parses a change — both current and previous project", () => {
    const event = parseProjectSystemEvent(
      issueMsg("m1", "channel_project_changed", {
        project_id: "proj-2",
        project_title: "New Home",
        previous_project_id: "proj-1",
        previous_project_title: "Old Home",
      }),
    );
    expect(event).toMatchObject({
      event: "channel_project_changed",
      projectId: "proj-2",
      projectTitle: "New Home",
      previousProjectId: "proj-1",
      previousProjectTitle: "Old Home",
    });
  });

  it("parses an unbind — previous project only, no current", () => {
    const event = parseProjectSystemEvent(
      issueMsg("m1", "channel_project_unbound", {
        previous_project_id: "proj-1",
        previous_project_title: "Old Home",
      }),
    );
    expect(event).toMatchObject({
      event: "channel_project_unbound",
      previousProjectId: "proj-1",
      previousProjectTitle: "Old Home",
    });
    expect(event?.projectId).toBeUndefined();
    expect(event?.projectTitle).toBeUndefined();
  });

  it("returns null when the row names neither a current nor a previous project", () => {
    expect(
      parseProjectSystemEvent(issueMsg("m1", "channel_project_bound", { actor_id: "agent-be" })),
    ).toBeNull();
  });

  it("ignores non-project system events", () => {
    expect(
      parseProjectSystemEvent(issueMsg("m1", "channel_member_added", { target_id: "user-2" })),
    ).toBeNull();
  });
});

describe("foldedIssueEventIds", () => {
  it("merges a same-source completed event with a status→done event (no double render)", () => {
    const statusDone = issueMsg("m1", "issue_status_changed", {
      issue_id: "issue-uuid",
      issue_identifier: "LRM-137",
      issue_status: "done",
    });
    const completed = issueMsg("m2", "issue_completed", {
      issue_id: "issue-uuid",
      issue_identifier: "LRM-137",
      issue_status: "done",
    });
    const folded = foldedIssueEventIds([statusDone, completed]);
    // The first row survives; the redundant "done" repeat is suppressed.
    expect([...folded]).toEqual(["m2"]);
  });

  it("keeps distinct verbs on the same task and events on different tasks", () => {
    const inProgress = issueMsg("m1", "issue_status_changed", {
      issue_id: "issue-a",
      issue_identifier: "LRM-1",
      issue_status: "in_progress",
    });
    const inReview = issueMsg("m2", "issue_status_changed", {
      issue_id: "issue-a",
      issue_identifier: "LRM-1",
      issue_status: "in_review",
    });
    const otherTaskDone = issueMsg("m3", "issue_completed", {
      issue_id: "issue-b",
      issue_identifier: "LRM-2",
      issue_status: "done",
    });
    expect(foldedIssueEventIds([inProgress, inReview, otherTaskDone]).size).toBe(0);
  });

  it("does not fold across a non-issue row between two identical events", () => {
    const first = issueMsg("m1", "issue_completed", {
      issue_id: "issue-a",
      issue_identifier: "LRM-1",
      issue_status: "done",
    });
    const memberRow = issueMsg("m2", "channel_member_added", { target_id: "user-2" });
    const second = issueMsg("m3", "issue_completed", {
      issue_id: "issue-a",
      issue_identifier: "LRM-1",
      issue_status: "done",
    });
    expect(foldedIssueEventIds([first, memberRow, second]).size).toBe(0);
  });
});

describe("IssueSystemEventContent", () => {
  beforeEach(() => openPanelMock.mockClear());

  const inProgressEvent: IssueSystemEvent = {
    event: "issue_status_changed",
    issueId: "issue-uuid",
    issueIdentifier: "LRM-137",
    issueStatus: "in_progress",
    previousStatus: "todo",
    actorId: "agent-be",
    actorType: "agent",
  };

  it("renders the frozen Issue copy with the issue ref as the SOLE link (item #7)", () => {
    render(<IssueSystemEventContent event={inProgressEvent} />);

    // Canonical example: "@后端工程师 开始了 LRM-137".
    expect(document.body.textContent).toBe("@后端工程师 开始了 LRM-137");

    // The issue identifier is the one and only clickable token.
    const links = document.querySelectorAll("a");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveTextContent("LRM-137");
    expect(links[0]).toHaveAttribute("data-issue-ref", "");

    // No raw enums leak into the copy.
    const text = document.body.textContent ?? "";
    expect(text).not.toContain("in_progress");
    expect(text).not.toContain("移到");
  });

  it("maps each transition to its frozen action verb", () => {
    const cases: Array<[Partial<IssueSystemEvent>, string]> = [
      [{ event: "issue_status_changed", issueStatus: "in_review" }, "@后端工程师 将 LRM-137 移至「评审」"],
      [{ event: "issue_completed", issueStatus: "done" }, "@后端工程师 完成了 LRM-137"],
      [{ event: "issue_status_changed", issueStatus: "done" }, "@后端工程师 完成了 LRM-137"],
      [{ event: "issue_status_changed", issueStatus: "blocked" }, "@后端工程师 将 LRM-137 移至「已阻塞」"],
    ];
    for (const [patch, expected] of cases) {
      const { unmount } = render(
        <IssueSystemEventContent event={{ ...inProgressEvent, ...patch }} />,
      );
      expect(document.body.textContent).toBe(expected);
      unmount();
    }
  });

  it("degrades an UNKNOWN status to a generic action — never leaks the raw enum (Nash)", () => {
    render(
      <IssueSystemEventContent
        event={{ ...inProgressEvent, event: "issue_status_changed", issueStatus: "triaging_v2" }}
      />,
    );
    const text = document.body.textContent ?? "";
    // Generic, status-less localized action…
    expect(text).toBe("@后端工程师 更新了 LRM-137");
    // …and the raw enum never reaches the user face.
    expect(text).not.toContain("triaging_v2");
  });

  it("renders assignee as a clickable @mention, with issue ref still its own link (LRM-306)", () => {
    render(
      <IssueSystemEventContent
        event={{
          event: "issue_assigned",
          issueId: "issue-uuid",
          issueIdentifier: "LRM-137",
          issueStatus: "todo",
          actorId: "agent-be",
          actorType: "agent",
          targetId: "user-2",
          targetType: "human",
          targetName: "Wendy",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@后端工程师 将 LRM-137 指派给 @wendy");
    // Issue ref stays its own <a>; actor + assignee are ActorMention tokens.
    const links = document.querySelectorAll("a");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveTextContent("LRM-137");
    expect(links[0]).toHaveAttribute("data-issue-ref", "");
    const tokens = screen.getAllByTestId("actor-token");
    expect(tokens).toHaveLength(2);
    expect(tokens[0]).toHaveAttribute("data-member-type", "agent");
    expect(tokens[0]).toHaveAttribute("data-member-id", "agent-be");
    expect(tokens[0]).toHaveTextContent("@后端工程师");
    expect(tokens[1]).toHaveAttribute("data-member-type", "member");
    expect(tokens[1]).toHaveAttribute("data-member-id", "user-2");
    expect(tokens[1]).toHaveTextContent("@wendy");
  });

  it("resolves group-manager actors via member-profile (DB), not emit-time actor_name", () => {
    // 贝克汉姆 is a group manager — ListAgents hides them (LRM-233). LRM-281 /
    // LRM-238 forbid actor_name fallback; the FE must fetch /member-profiles.
    render(
      <IssueSystemEventContent
        event={{
          event: "issue_assigned",
          issueId: "issue-uuid",
          issueIdentifier: "LRM-268",
          issueStatus: "todo",
          actorId: "agent-beckham",
          actorType: "agent",
          // Deliberately wrong emit-time names — must not be used.
          actorName: "SHOULD_NOT_APPEAR",
          actorHandle: "should-not-appear",
          targetId: "agent-fe",
          targetType: "agent",
          targetName: "ALSO_WRONG",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@贝克汉姆 将 LRM-268 指派给 @前端工程师");
    expect(document.body.textContent).not.toContain("Unknown Agent");
    expect(document.body.textContent).not.toContain("SHOULD_NOT_APPEAR");
    expect(document.body.textContent).not.toContain("ALSO_WRONG");
    const tokens = screen.getAllByTestId("actor-token");
    expect(tokens).toHaveLength(2);
    const assigneeToken = tokens[1]!;
    expect(assigneeToken).toHaveAttribute("data-member-type", "agent");
    expect(assigneeToken).toHaveAttribute("data-member-id", "agent-fe");
    fireEvent.click(assigneeToken);
    expect(openPanelMock).toHaveBeenCalledWith("agent-fe");
  });

  it("uses assigned_unknown when typed target facts are missing (LRM-306 / LRM-238)", () => {
    render(
      <IssueSystemEventContent
        event={{
          event: "issue_assigned",
          issueId: "issue-uuid",
          issueIdentifier: "LRM-137",
          issueStatus: "todo",
          actorId: "agent-be",
          actorType: "agent",
          // No targetId / targetType — never invent a clickable identity.
          targetName: "Wendy",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@后端工程师 重新指派了 LRM-137");
    expect(document.body.textContent).not.toContain("Wendy");
    expect(document.body.textContent).not.toContain("@wendy");
    const tokens = screen.getAllByTestId("actor-token");
    expect(tokens).toHaveLength(1);
    expect(tokens[0]).toHaveAttribute("data-member-id", "agent-be");
  });

  it("keeps a typed assignee clickable when directory/profile miss (no name fallback)", () => {
    render(
      <IssueSystemEventContent
        event={{
          event: "issue_assigned",
          issueId: "issue-uuid",
          issueIdentifier: "LRM-137",
          issueStatus: "todo",
          actorId: "agent-be",
          actorType: "agent",
          targetId: "ghost-x",
          targetType: "human",
          targetHandle: "ghost",
          targetName: "Ghost",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@后端工程师 将 LRM-137 指派给 @ghost-x");
    expect(document.body.textContent).not.toContain("Ghost");
    const tokens = screen.getAllByTestId("actor-token");
    expect(tokens).toHaveLength(2);
    expect(tokens[1]).toHaveAttribute("data-member-type", "member");
    expect(tokens[1]).toHaveAttribute("data-member-id", "ghost-x");
  });

  it("renders issue_created as a fixed verb with the ref as the SOLE link (#610)", () => {
    render(
      <IssueSystemEventContent
        event={{
          event: "issue_created",
          issueId: "issue-uuid",
          issueIdentifier: "LRM-200",
          actorId: "agent-be",
          actorType: "agent",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@后端工程师 创建了 LRM-200");
    const links = document.querySelectorAll("a");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveTextContent("LRM-200");
  });

  it("uses title-primary on the main row when issueTitle is stamped (LRM-609 A')", () => {
    render(
      <IssueSystemEventContent
        event={{
          ...inProgressEvent,
          issueTitle: "Soft system rows",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@后端工程师 开始了 Soft system rows");
    const links = document.querySelectorAll("a");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveTextContent("Soft system rows");
    expect(links[0]).not.toHaveTextContent("LRM-137");
  });
});

describe("ProjectSystemEventContent", () => {
  const boundEvent: ProjectSystemEvent = {
    event: "channel_project_bound",
    projectId: "proj-1",
    projectTitle: "Q3 Roadmap",
    actorId: "agent-be",
    actorType: "agent",
  };

  it("renders a bind with the current project as the SOLE clickable object (#610)", () => {
    render(<ProjectSystemEventContent event={boundEvent} />);
    expect(document.body.textContent).toBe("@后端工程师 把本群关联到项目「Q3 Roadmap」");
    const links = document.querySelectorAll("a");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveTextContent("Q3 Roadmap");
    expect(links[0]).toHaveAttribute("href", "/ws/projects/proj-1");
    // The actor is its own @mention token — never inside the project link.
    expect(links[0]).not.toHaveTextContent("后端工程师");
  });

  it("links only the NEW project on a change; the previous name stays plain text", () => {
    render(
      <ProjectSystemEventContent
        event={{
          event: "channel_project_changed",
          projectId: "proj-2",
          projectTitle: "New Home",
          previousProjectId: "proj-1",
          previousProjectTitle: "Old Home",
          actorId: "agent-be",
          actorType: "agent",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@后端工程师 把关联项目从「Old Home」改为「New Home」");
    const links = document.querySelectorAll("a");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveTextContent("New Home");
    expect(links[0]).toHaveAttribute("href", "/ws/projects/proj-2");
  });

  it("links the previous project on an unbind — its only object (Barry's contract)", () => {
    render(
      <ProjectSystemEventContent
        event={{
          event: "channel_project_unbound",
          previousProjectId: "proj-1",
          previousProjectTitle: "Old Home",
          actorId: "agent-be",
          actorType: "agent",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@后端工程师 解除了与项目「Old Home」的关联");
    const links = document.querySelectorAll("a");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveTextContent("Old Home");
    expect(links[0]).toHaveAttribute("href", "/ws/projects/proj-1");
  });

  it("degrades to plain text when the project id is absent — never a dead link", () => {
    render(
      <ProjectSystemEventContent
        event={{
          event: "channel_project_bound",
          projectTitle: "Untethered",
          actorId: "agent-be",
          actorType: "agent",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@后端工程师 把本群关联到项目「Untethered」");
    expect(document.querySelectorAll("a")).toHaveLength(0);
  });

  it("does not use emit-time actor_name on directory/profile miss (LRM-281)", () => {
    render(
      <ProjectSystemEventContent
        event={{
          event: "channel_project_bound",
          projectId: "proj-1",
          projectTitle: "Q3 Roadmap",
          actorId: "ghost-x",
          actorType: "human",
          actorName: "Lin",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@ghost-x 把本群关联到项目「Q3 Roadmap」");
    expect(document.body.textContent).not.toContain("Lin");
  });
});

describe("parseThreadSystemEvent", () => {
  it("parses thread_unfollowed with actor facts (LRM-540)", () => {
    const event = parseThreadSystemEvent(
      systemMessage({
        event: "thread_unfollowed",
        params: {
          actor_id: "agent-beckham",
          actor_type: "agent",
          actor_handle: "bei-ke-han-mu-11",
          actor_display_name: "贝克汉姆",
          agent_id: "agent-beckham",
        },
      }),
    );
    expect(event).toEqual({
      event: "thread_unfollowed",
      actorId: "agent-beckham",
      actorType: "agent",
      actorHandle: "bei-ke-han-mu-11",
      actorName: undefined,
    });
  });

  it("accepts legacy agent_id-only params as an agent actor", () => {
    const event = parseThreadSystemEvent(
      systemMessage({
        event: "thread_unfollowed",
        params: { agent_id: "agent-fe", agent_name: "前端工程师" },
      }),
    );
    expect(event).toMatchObject({
      event: "thread_unfollowed",
      actorId: "agent-fe",
      actorType: "agent",
      actorName: "前端工程师",
    });
  });

  it("ignores rows without a resolvable actor id", () => {
    expect(
      parseThreadSystemEvent(systemMessage({ event: "thread_unfollowed", params: {} })),
    ).toBeNull();
  });

  it("ignores non-thread system events", () => {
    expect(
      parseThreadSystemEvent(systemMessage({ event: "channel_member_left", params: { target_id: "u1" } })),
    ).toBeNull();
  });
});

describe("ThreadSystemEventContent (LRM-540)", () => {
  const unfollowEvent: ThreadSystemEvent = {
    event: "thread_unfollowed",
    actorId: "agent-beckham",
    actorType: "agent",
    actorHandle: "bei-ke-han-mu-11",
  };

  it("renders @display_name primary ink, not the handle slug", () => {
    render(<ThreadSystemEventContent event={unfollowEvent} />);
    expect(document.body.textContent).toBe("@贝克汉姆 取消关注了此话题");
    expect(document.body.textContent).not.toContain("bei-ke-han-mu-11");
    const token = screen.getByTestId("actor-token");
    expect(token).toHaveAttribute("data-member-type", "agent");
    expect(token).toHaveAttribute("data-member-id", "agent-beckham");
    expect(token).toHaveTextContent("@贝克汉姆");
  });

  it("falls back to @handle (never uuid) when the actor cannot be resolved", () => {
    render(
      <ThreadSystemEventContent
        event={{
          event: "thread_unfollowed",
          actorId: "agent-missing",
          actorType: "agent",
          actorHandle: "some-handle",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@some-handle 取消关注了此话题");
    expect(document.body.textContent).not.toContain("agent-missing");
  });

  it("renders the followed template when the event is thread_followed", () => {
    render(
      <ThreadSystemEventContent
        event={{
          event: "thread_followed",
          actorId: "agent-fe",
          actorType: "agent",
          actorHandle: "qian-duan",
        }}
      />,
    );
    expect(document.body.textContent).toBe("@前端工程师 关注了此话题");
  });
});
