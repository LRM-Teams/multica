import type { ReactNode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelMessage, MessagePart } from "@multica/core/types";
import {
  parseMemberSystemEvent,
  parseIssueSystemEvent,
  parseProjectSystemEvent,
  foldedIssueEventIds,
  type IssueSystemEvent,
  type ProjectSystemEvent,
} from "./channel-system-event";
import {
  MemberSystemEventContent,
  IssueSystemEventContent,
  ProjectSystemEventContent,
} from "./channel-system-event-content";

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

// Issue rows resolve the actor/assignee display name from the identity cache.
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    // Mirrors the real resolver's priority: a cache hit (by id, across both
    // agents and members) wins over whatever display-name fallback the
    // caller passed in; "agent-be" is an Issue-test-only fixture that's
    // deliberately absent from mockAgents so it always falls through to the
    // caller-supplied fallback.
    getActorName: (_type: string, id: string, fallback?: string) => {
      if (id === "agent-be") return "后端工程师";
      const agent = mockAgents.find((a) => a.id === id);
      if (agent) return agent.handle;
      const member = mockMembers.find((m) => m.user_id === id);
      if (member) return member.handle;
      return fallback ?? "Someone";
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
            member_removed: "{target} was removed from this channel by {actor}",
            member_left: "{target} left this channel",
            issue: {
              actor_system: "Multica",
              created: "{actor} 创建了 Issue {issue}",
              assigned: "{actor} 将 Issue {issue} 指派给 {{target}}",
              assigned_unknown: "{actor} 重新指派了 Issue {issue}",
              in_progress: "{actor} 将 Issue {issue} 标记为处理中",
              in_review: "{actor} 将 Issue {issue} 提交审核",
              done: "{actor} 完成了 Issue {issue}",
              updated: "{actor} 更新了 Issue {issue}",
              status: "{actor} 将 Issue {issue} 标记为{{status}}",
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

    // Canonical example: "@后端工程师 将 Issue LRM-137 标记为处理中".
    expect(document.body.textContent).toBe("@后端工程师 将 Issue LRM-137 标记为处理中");

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
      [{ event: "issue_status_changed", issueStatus: "in_review" }, "@后端工程师 将 Issue LRM-137 提交审核"],
      [{ event: "issue_completed", issueStatus: "done" }, "@后端工程师 完成了 Issue LRM-137"],
      [{ event: "issue_status_changed", issueStatus: "done" }, "@后端工程师 完成了 Issue LRM-137"],
      [{ event: "issue_status_changed", issueStatus: "blocked" }, "@后端工程师 将 Issue LRM-137 标记为已阻塞"],
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
    expect(text).toBe("@后端工程师 更新了 Issue LRM-137");
    // …and the raw enum never reaches the user face.
    expect(text).not.toContain("triaging_v2");
  });

  it("names the assignee for an assignment, still with only the ref linked", () => {
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
    expect(document.body.textContent).toBe("@后端工程师 将 Issue LRM-137 指派给 wendy");
    const links = document.querySelectorAll("a");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveTextContent("LRM-137");
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
    expect(document.body.textContent).toBe("@后端工程师 创建了 Issue LRM-200");
    const links = document.querySelectorAll("a");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveTextContent("LRM-200");
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

  it("uses the backend actor display name on a cache miss — no bare handle leak", () => {
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
    expect(document.body.textContent).toBe("@Lin 把本群关联到项目「Q3 Roadmap」");
  });
});
