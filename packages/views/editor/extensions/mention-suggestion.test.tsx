import { fireEvent, render, screen } from "@testing-library/react";
import { createRef, type ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { issueKeys, PAGINATED_STATUSES } from "@multica/core/issues/queries";
import { buildGroupMentionAllowedActorIds } from "../../channels/mention-scope";
import { I18nProvider } from "@multica/core/i18n/react";
import type { IssueStatus, ListIssuesCache } from "@multica/core/types";
import type { QueryClient } from "@tanstack/react-query";
import enCommon from "../../locales/en/common.json";
import enAuth from "../../locales/en/auth.json";
import enSettings from "../../locales/en/settings.json";
import enEditor from "../../locales/en/editor.json";

const TEST_RESOURCES = {
  en: { common: enCommon, auth: enAuth, settings: enSettings, editor: enEditor },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

// Mock the workspace id singleton — items() reads it imperatively.
vi.mock("@multica/core/platform", () => ({
  getCurrentWsId: () => "ws-1",
}));

// Mock the API so we control search responses + observe calls.
const searchIssuesMock = vi.fn();
const searchProjectsMock = vi.fn();
const actorProfileTriggerMock = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({
  api: {
    get searchIssues() {
      return searchIssuesMock;
    },
    get searchProjects() {
      return searchProjectsMock;
    },
  },
}));

// Mock the auth store: items() reads `useAuthStore.getState()` imperatively
// to identify the current user when filtering personal agents.
const authState = { user: { id: "u1" } as { id: string } | null };
vi.mock("@multica/core/auth", () => ({
  useAuthStore: { getState: () => authState },
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid="actor-avatar" data-actor-id={actorId} />
  ),
}));

vi.mock("../../common/actor-profile-popover", () => ({
  ActorProfileTrigger: ({ children }: { children: ReactNode }) => {
    actorProfileTriggerMock();
    return <span data-testid="actor-profile-trigger">{children}</span>;
  },
}));

import {
  createMentionSuggestion,
  MentionList,
  type MentionListRef,
  type MentionItem,
} from "./mention-suggestion";

function fakeQc(data: {
  members?: Array<{ user_id: string; name: string; display_name?: string; role?: string }>;
  agents?: Array<{
    id: string;
    name: string;
    display_name?: string;
    archived_at: string | null;
    visibility?: "workspace" | "private";
    owner_id?: string | null;
  }>;
  squads?: never;
  issues?: Array<{ id: string; identifier: string; title: string; status: string }>;
  /** When true, leave the member/agent directory keys absent (true cold cache)
   *  instead of seeding them with empty arrays (empty-but-resolved cache). */
  coldDirectory?: boolean;
}): QueryClient {
  const map = new Map<string, unknown>();
  if (!data.coldDirectory) {
    map.set(JSON.stringify(workspaceKeys.members("ws-1")), data.members ?? []);
    map.set(JSON.stringify(workspaceKeys.agents("ws-1")), data.agents ?? []);
  }
  const byStatus: ListIssuesCache["byStatus"] = {};
  for (const status of PAGINATED_STATUSES) {
    const bucket = (data.issues ?? []).filter((i) => i.status === status);
    byStatus[status as IssueStatus] = { issues: bucket as never, total: bucket.length };
  }
  map.set(
    JSON.stringify(issueKeys.list("ws-1")),
    { byStatus } satisfies ListIssuesCache,
  );
  return {
    getQueryData: (key: readonly unknown[]) => map.get(JSON.stringify(key)),
    getQueriesData: <T,>(filter: { queryKey: readonly unknown[] }) => {
      const prefix = filter.queryKey as unknown[];
      const results: [readonly unknown[], T][] = [];
      for (const [k, v] of map) {
        const parsed = JSON.parse(k) as unknown[];
        if (parsed.length >= prefix.length && prefix.every((seg, i) => JSON.stringify(seg) === JSON.stringify(parsed[i]))) {
          results.push([parsed, v as T]);
        }
      }
      return results;
    },
    prefetchQuery: prefetchQuerySpy,
  } as unknown as QueryClient;
}

// Module-level spy the fake query client forwards prefetchQuery to, so tests
// can assert the bare-@ cold-cache path warms the mention directory without
// requiring the full React Query machinery.
const prefetchQuerySpy = vi.hoisted(() => vi.fn());

describe("createMentionSuggestion", () => {
  beforeEach(() => {
    searchIssuesMock.mockReset();
    searchProjectsMock.mockReset();
    actorProfileTriggerMock.mockClear();
    prefetchQuerySpy.mockReset();
    Element.prototype.scrollIntoView = vi.fn();
  });

  it("returns a cold-cache bare `@` pool by warming the member/agent directory", () => {
    // Cold cache: no members/agents resolved yet in the React Query store.
    const qc = fakeQc({ coldDirectory: true });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc);
    const result = config.items!({ query: "", editor: {} as never }) as MentionItem[];

    // Synchronous result may be empty on first cold `@`, but the directory
    // must be on its way so the picker surfaces candidates instead of a dead
    // "no results" state — i.e. prefetch is fired for both lists, once each.
    expect(Array.isArray(result)).toBe(true);
    expect(prefetchQuerySpy).toHaveBeenCalledTimes(2);
    const keys = prefetchQuerySpy.mock.calls.map((c) => JSON.stringify(c[0]?.queryKey));
    expect(keys).toContain(JSON.stringify(["workspaces", "ws-1", "members"]));
    expect(keys).toContain(JSON.stringify(["workspaces", "ws-1", "agents"]));
  });

  it("returns members and agents synchronously without waiting for the server search", () => {
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "alice", display_name: "Alice", role: "member" }],
      agents: [
        {
          id: "a1",
          name: "agent_aegis",
          display_name: "Aegis",
          archived_at: null,
          visibility: "workspace",
          owner_id: null,
        },
      ],
    });
    // A pending fetch — would block the result if items() awaited it.
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc);
    const result = config.items!({ query: "a", editor: {} as never });

    // Must be synchronous: a plain array, not a Promise.
    expect(Array.isArray(result)).toBe(true);
    const items = result as MentionItem[];
    expect(items).toContainEqual(expect.objectContaining({
      type: "member",
      label: "Alice",
      handle: "alice",
      secondaryLabel: "@alice",
    }));
    expect(items).toContainEqual(expect.objectContaining({
      type: "agent",
      label: "Aegis",
      handle: "agent_aegis",
      secondaryLabel: "@agent_aegis",
    }));
  });

  it("renders broadcast as a top row and keeps members and agents in one section", () => {
    render(
      <I18nWrapper>
        <MentionList
          items={[
            { id: "all", label: "all", type: "all" },
            { id: "u1", label: "Alice", type: "member", handle: "alice", secondaryLabel: "@alice" },
            { id: "a1", label: "Aegis", type: "agent", handle: "agent_aegis", secondaryLabel: "@agent_aegis" },
          ]}
          query=""
          command={vi.fn()}
        />
      </I18nWrapper>,
    );

    expect(screen.getByText("All members")).toBeInTheDocument();
    expect(screen.getByText("Notify everyone in this conversation")).toBeInTheDocument();
    expect(screen.getByText("Members")).toBeInTheDocument();
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("Aegis")).toBeInTheDocument();
    expect(screen.queryByText("All")).not.toBeInTheDocument();
    expect(screen.queryByText("Users")).not.toBeInTheDocument();
  });

  it("keeps the display label when selecting an actor with a duplicate display name", () => {
    const command = vi.fn();
    render(
      <I18nWrapper>
        <MentionList
          items={[
            { id: "a-wendy-1", label: "Wendy", type: "agent", handle: "wendy", secondaryLabel: "@wendy" },
            { id: "a-wendy-2", label: "Wendy", type: "agent", handle: "wendy_2", secondaryLabel: "@wendy_2" },
          ]}
          query="wendy"
          command={command}
        />
      </I18nWrapper>,
    );

    expect(screen.getByText("@wendy")).toBeInTheDocument();
    fireEvent.click(screen.getByText("@wendy_2").closest("button")!);
    expect(command).toHaveBeenCalledWith(expect.objectContaining({
      id: "a-wendy-2",
      label: "Wendy",
      handle: "wendy_2",
      type: "agent",
    }));
  });

  it("does not attach profile hover cards to member and agent picker rows", () => {
    render(
      <I18nWrapper>
        <MentionList
          items={[
            { id: "u1", label: "Alice", type: "member", handle: "alice", secondaryLabel: "@alice" },
            { id: "a1", label: "Aegis", type: "agent", handle: "agent_aegis", secondaryLabel: "@agent_aegis" },
          ]}
          query=""
          command={vi.fn()}
        />
      </I18nWrapper>,
    );

    expect(actorProfileTriggerMock).not.toHaveBeenCalled();
    expect(screen.queryByTestId("actor-profile-trigger")).not.toBeInTheDocument();
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("Aegis")).toBeInTheDocument();
  });

  it("matches handles and ranks handle matches before display-name-only matches", () => {
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "alice", display_name: "Alice", role: "member" }],
      agents: [
        {
          id: "a-display",
          name: "zeta",
          display_name: "Atlas",
          archived_at: null,
          visibility: "workspace",
          owner_id: null,
        },
        {
          id: "a-handle",
          name: "atlas",
          display_name: "Support",
          archived_at: null,
          visibility: "workspace",
          owner_id: null,
        },
      ],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc);
    const items = config.items!({ query: "atlas", editor: {} as never }) as MentionItem[];

    expect(items.filter((i) => i.type === "agent").map((i) => i.id)).toEqual([
      "a-handle",
      "a-display",
    ]);
  });

  it("does not load server issue matches in the normal @ picker", async () => {
    searchIssuesMock.mockResolvedValue({
      issues: [
        {
          id: "i-1007",
          identifier: "MUL-1007",
          title: "多 Agent 协作探索",
          status: "done",
        },
      ],
      total: 1,
    });

    render(<I18nWrapper><MentionList items={[]} query="协作" command={vi.fn()} /></I18nWrapper>);

    expect(screen.getByText("No results")).toBeInTheDocument();
    expect(screen.queryByText("MUL-1007")).not.toBeInTheDocument();
    expect(searchIssuesMock).not.toHaveBeenCalled();
  });



  it("does not call searchIssues for an empty query", () => {
    render(<I18nWrapper><MentionList items={[]} query="" command={vi.fn()} /></I18nWrapper>);

    expect(searchIssuesMock).not.toHaveBeenCalled();
    expect(searchProjectsMock).not.toHaveBeenCalled();
  });

  it("captures Enter while the popup has no selectable items", () => {
    const ref = createRef<MentionListRef>();

    render(<I18nWrapper><MentionList ref={ref} items={[]} query="协作" command={vi.fn()} /></I18nWrapper>);

    expect(
      ref.current?.onKeyDown({ event: new KeyboardEvent("keydown", { key: "Enter" }) }),
    ).toBe(true);
  });

  // Retiring agent visibility removes the client-side owner/role discrimination
  // here. That is safe *today* because it was never the effective boundary:
  // `ListAgents` (server/internal/handler/agent.go:800) drops another member's
  // private agent before it reaches the client, so these fixtures describe a
  // response production does not produce for a regular member.
  //
  // ⚠️ When #908 removes that server filter, this stops being a no-op and
  // becomes a real product change — every workspace agent becomes mentionable
  // by every member. Flagged to Parker; reversing it is one rule function.
  it("offers every agent the server returned, whoever owns it", () => {
    const qc = fakeQc({
      members: [
        { user_id: "u1", name: "Alice", role: "member" },
        { user_id: "u2", name: "Bob", role: "member" },
      ],
      agents: [
        // Bob's personal agent. The server would not send this to Alice; if it
        // does arrive, the client no longer second-guesses it.
        {
          id: "a-personal-bob",
          name: "Atlas",
          archived_at: null,
          visibility: "private",
          owner_id: "u2",
        },
        // Alice's own personal agent — should be visible.
        {
          id: "a-personal-alice",
          name: "Athena",
          archived_at: null,
          visibility: "private",
          owner_id: "u1",
        },
        // Workspace agent — visible to everyone.
        {
          id: "a-shared",
          name: "Aether",
          archived_at: null,
          visibility: "workspace",
          owner_id: "u2",
        },
      ],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc);
    const result = config.items!({ query: "a", editor: {} as never });
    const items = result as MentionItem[];

    expect(items.some((i) => i.type === "agent" && i.label === "Athena")).toBe(true);
    expect(items.some((i) => i.type === "agent" && i.label === "Aether")).toBe(true);
    expect(items.some((i) => i.type === "agent" && i.label === "Atlas")).toBe(true);
  });

  // Kept as a regression guard on the admin path specifically: it passed before
  // and after, so it pins that widening the member case did not accidentally
  // narrow the admin one.
  it("shows everyone's personal agents to a workspace admin", () => {
    const qc = fakeQc({
      members: [
        { user_id: "u1", name: "Alice", role: "admin" },
        { user_id: "u2", name: "Bob", role: "member" },
      ],
      agents: [
        {
          id: "a-personal-bob",
          name: "Atlas",
          archived_at: null,
          visibility: "private",
          owner_id: "u2",
        },
      ],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc);
    const result = config.items!({ query: "a", editor: {} as never });
    const items = result as MentionItem[];

    expect(items.some((i) => i.type === "agent" && i.label === "Atlas")).toBe(true);
  });

  it("surfaces a channel co-member's private agent in channel scope", () => {
    // Bob's private Wendy is a member of the channel but not in Alice's
    // personal agent list. In a channel, membership — not assignability —
    // authorizes the mention, so scoped channel-member agents must appear.
    const qc = fakeQc({
      members: [
        { user_id: "u1", name: "Alice", role: "member" },
        { user_id: "u2", name: "Bob", role: "member" },
      ],
      agents: [
        // Alice's own agent — visible as usual.
        {
          id: "a-personal-alice",
          name: "Athena",
          archived_at: null,
          visibility: "private",
          owner_id: "u1",
        },
      ],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc, {
      getAllowedActorIds: () => new Set(["u1", "u2", "a-personal-alice", "wendy-bob"]),
      getScopedAgents: () => [
        // Bob's private Wendy — injected as a channel-member candidate.
        { id: "wendy-bob", name: "wendy", display_name: "Wendy" },
      ],
    });
    const result = config.items!({ query: "", editor: {} as never }) as MentionItem[];

    expect(result.some((i) => i.type === "agent" && i.label === "Wendy")).toBe(true);
    // Alice's own agent is still reachable in channel scope too.
    expect(result.some((i) => i.type === "agent" && i.label === "Athena")).toBe(true);
  });

  it("does not surface scoped agents outside channel scope", () => {
    // Without getAllowedActorIds (e.g. issue comment composer), scoped agents
    // are ignored and the assignability gate still hides others' private agents.
    const qc = fakeQc({
      members: [
        { user_id: "u1", name: "Alice", role: "member" },
      ],
      agents: [
        {
          id: "a-personal-alice",
          name: "Athena",
          archived_at: null,
          visibility: "private",
          owner_id: "u1",
        },
      ],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc, {
      getScopedAgents: () => [{ id: "wendy-bob", name: "wendy", display_name: "Wendy" }],
    });
    const result = config.items!({ query: "wen", editor: {} as never }) as MentionItem[];

    expect(result.some((i) => i.type === "agent" && i.label === "Wendy")).toBe(false);
  });

  it("excludes cached issues from the normal @ response", () => {
    const qc = fakeQc({
      issues: [
        { id: "i1", identifier: "MUL-1", title: "Login bug", status: "todo" },
        { id: "i2", identifier: "MUL-2", title: "Other", status: "done" },
      ],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc);
    const result = config.items!({ query: "bug", editor: {} as never });

    const items = result as MentionItem[];
    expect(items.some((i) => i.type === "issue")).toBe(false);
  });

  it("does not inject current/recent chat context into the normal @ results", () => {
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      issues: [{ id: "i1", identifier: "MUL-1", title: "Login bug", status: "todo" }],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc);
    const result = config.items!({ query: "", editor: {} as never }) as MentionItem[];

    expect(result.some((item) => item.group === "current" || item.group === "recent")).toBe(false);
    expect(result.map((item) => `${item.type}:${item.id}`)).toContain("member:u1");
    expect(result.map((item) => `${item.type}:${item.id}`)).not.toContain("issue:i1");
  });


  it("shows only current/recent chat context before the user types a query", () => {
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [{ id: "a1", name: "Aegis", archived_at: null, visibility: "workspace", owner_id: null }],
      issues: [{ id: "i-cache", identifier: "MUL-9", title: "Cached", status: "todo" }],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc, {
      mode: "context",
      getContextItems: () => [
        { id: "i1", label: "MUL-1", type: "issue", description: "Alpha issue", status: "todo", group: "current" },
        { id: "p1", label: "Roadmap", type: "project", description: "Q3", group: "recent" },
      ],
    });
    const result = config.items!({ query: "", editor: {} as never }) as MentionItem[];

    expect(result.map((item) => `${item.type}:${item.id}`)).toEqual(["issue:i1", "project:p1"]);
    expect(result.some((item) => item.type === "member" || item.type === "agent")).toBe(false);
  });

  it("prepends current/recent chat context without removing normal mention targets after the user types", () => {
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [{ id: "a1", name: "Aegis", archived_at: null, visibility: "workspace", owner_id: null }],
      issues: [{ id: "i-cache", identifier: "MUL-9", title: "Cached", status: "todo" }],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc, {
      mode: "context",
      getContextItems: () => [
        { id: "i1", label: "MUL-1", type: "issue", description: "Alpha issue", status: "todo", group: "current" },
        { id: "p1", label: "Roadmap", type: "project", description: "Q3", group: "recent" },
      ],
    });
    const result = config.items!({ query: "a", editor: {} as never }) as MentionItem[];

    expect(result.map((item) => `${item.type}:${item.id}`).slice(0, 2)).toEqual(["issue:i1", "project:p1"]);
    expect(result.some((item) => item.type === "member" && item.label === "Alice")).toBe(true);
    expect(result.some((item) => item.type === "agent" && item.label === "Aegis")).toBe(true);
  });

  it("renders current and recent sections for injected object mentions", () => {
    render(
      <I18nWrapper>
        <MentionList
          items={[
            { id: "i1", label: "MUL-1", type: "issue", description: "Login bug", group: "current" },
            { id: "p1", label: "Roadmap", type: "project", description: "Q3", group: "recent" },
          ]}
          query=""
          command={vi.fn()}
        />
      </I18nWrapper>,
    );

    expect(screen.getByText("Current page")).toBeInTheDocument();
    expect(screen.getByText("Recently viewed")).toBeInTheDocument();
    expect(screen.getByText("MUL-1")).toBeInTheDocument();
    expect(screen.getByText("Roadmap")).toBeInTheDocument();
  });

  it("never offers squads in the composer @ picker — candidates are member/agent only (bare-mention cutover, Barry #605 gate)", () => {
    // Even with squads cached, they must not appear: the server has no bare-squad
    // parse contract, so a picked squad would serialize to bare `@name` that never
    // resolves into a structured/routing ref — a silent no-op. Restore when a BE
    // squad bare-token contract exists.
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc);
    const items = config.items!({ query: "", editor: {} as never }) as MentionItem[];

    expect(items.filter((i) => i.type === "squad")).toHaveLength(0);
    expect(items.some((i) => i.type === "member" && i.label === "Alice")).toBe(true);
  });

  it("matches Chinese names by full pinyin", () => {
    const qc = fakeQc({
      members: [
        { user_id: "u1", name: "Alice", role: "member" },
        { user_id: "u2", name: "李云龙", role: "member" },
      ],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc);
    const result = config.items!({ query: "liyunlong", editor: {} as never });

    const items = result as MentionItem[];
    expect(items.some((i) => i.type === "member" && i.label === "李云龙")).toBe(true);
    expect(items.some((i) => i.type === "member" && i.label === "Alice")).toBe(false);
  });

  it("matches Chinese names by pinyin initials", () => {
    const qc = fakeQc({
      members: [
        { user_id: "u1", name: "Alice", role: "member" },
        { user_id: "u2", name: "李云龙", role: "member" },
        { user_id: "u3", name: "张大彪", role: "member" },
      ],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc);
    const result = config.items!({ query: "lyl", editor: {} as never });

    const items = result as MentionItem[];
    expect(items.some((i) => i.type === "member" && i.label === "李云龙")).toBe(true);
    expect(items.some((i) => i.type === "member" && i.label === "张大彪")).toBe(false);
  });

  it("matches Chinese agent names by pinyin", () => {
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        { id: "a1", name: "魏和尚", archived_at: null, visibility: "workspace", owner_id: null },
      ],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc);
    const result = config.items!({ query: "whs", editor: {} as never });

    const items = result as MentionItem[];
    expect(items.some((i) => i.type === "agent" && i.label === "魏和尚")).toBe(true);
  });

  // #547 contract: `name` is the machine @handle (English/ASCII) and
  // `display_name` is the human label (may be Chinese). The picker must show
  // display_name and find the agent by EITHER field. The inserted node still
  // carries the stable handle (`name`) — display changes never touch routing.
  it("displays display_name and matches by both display_name and handle (#547)", () => {
    const mkQc = () =>
      fakeQc({
        members: [{ user_id: "u1", name: "alice", display_name: "Alice", role: "member" }],
        agents: [
          {
            id: "a1",
            name: "beckham",
            display_name: "贝克汉姆",
            archived_at: null,
            visibility: "workspace",
            owner_id: null,
          },
        ],
      });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    // Chinese display_name substring finds the agent…
    const byDisplay = createMentionSuggestion(mkQc()).items!({
      query: "贝克",
      editor: {} as never,
    }) as MentionItem[];
    const displayHit = byDisplay.find((i) => i.type === "agent" && i.id === "a1");
    expect(displayHit).toBeDefined();
    // …and the visible label is the display_name, while the inserted handle
    // stays the stable English @handle.
    expect(displayHit?.label).toBe("贝克汉姆");
    expect(displayHit?.handle).toBe("beckham");

    // English handle also finds the same agent.
    const byHandle = createMentionSuggestion(mkQc()).items!({
      query: "beckham",
      editor: {} as never,
    }) as MentionItem[];
    expect(byHandle.some((i) => i.type === "agent" && i.id === "a1")).toBe(true);
  });

  it("#35: tags actors in_channel vs not_in_channel when membership set is provided", () => {
    const qc = fakeQc({
      members: [
        { user_id: "u-in", name: "in-channel", role: "member" },
        { user_id: "u-out", name: "not-in-channel", role: "member" },
      ],
      agents: [
        {
          id: "a-in",
          name: "agent-in",
          display_name: "Agent In",
          archived_at: null,
          visibility: "workspace",
          owner_id: null,
        },
        {
          id: "a-out",
          name: "agent-out",
          display_name: "Agent Out",
          archived_at: null,
          visibility: "workspace",
          owner_id: null,
        },
      ],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    // Group channel: full workspace allowed + membership for sections.
    const config = createMentionSuggestion(qc, {
      getAllowedActorIds: () => new Set(["u-in", "u-out", "a-in", "a-out"]),
      getChannelMemberIds: () => new Set(["u-in", "a-in"]),
    });
    const items = config.items!({ query: "", editor: {} as never }) as MentionItem[];

    expect(items.find((i) => i.id === "u-in")?.group).toBe("in_channel");
    expect(items.find((i) => i.id === "a-in")?.group).toBe("in_channel");
    expect(items.find((i) => i.id === "u-out")?.group).toBe("not_in_channel");
    expect(items.find((i) => i.id === "a-out")?.group).toBe("not_in_channel");
  });

  it("does not offer the viewing human in a group-channel @ picker", () => {
    const qc = fakeQc({
      members: [
        { user_id: "u1", name: "alice", display_name: "Alice", role: "member" },
        { user_id: "u2", name: "bob", display_name: "Bob", role: "member" },
      ],
      agents: [
        {
          id: "a1",
          name: "aegis",
          display_name: "Aegis",
          archived_at: null,
          visibility: "workspace",
          owner_id: null,
        },
      ],
    });
    searchIssuesMock.mockReturnValue(new Promise(() => {}));

    const config = createMentionSuggestion(qc, {
      getAllowedActorIds: () =>
        buildGroupMentionAllowedActorIds({
          workspaceUserIds: ["u1", "u2"],
          workspaceAgentIds: ["a1"],
          channelMemberIds: ["u1", "u2", "a1"],
          viewerUserId: "u1",
        }),
    });
    const items = config.items!({ query: "", editor: {} as never }) as MentionItem[];

    expect(items.some((i) => i.type === "member" && i.id === "u1")).toBe(false);
    expect(items.some((i) => i.type === "member" && i.id === "u2")).toBe(true);
    expect(items.some((i) => i.type === "agent" && i.id === "a1")).toBe(true);
  });

  it("#2115: selecting a regrouped in-channel row inserts that actor, not displayItems[index]", () => {
    // Candidate order puts NOT-in-channel first; groupItems renders InChannel first.
    // Pre-fix selectItem used displayItems[index], so clicking visual row 0 inserted
    // the wrong person (wrong agent woken in group chat).
    const command = vi.fn();
    const ref = createRef<MentionListRef>();
    render(
      <I18nWrapper>
        <MentionList
          ref={ref}
          items={[
            {
              id: "a-out",
              label: "Not In Channel Agent",
              type: "agent",
              handle: "agent_out",
              secondaryLabel: "@agent_out",
              group: "not_in_channel",
            },
            {
              id: "a-in",
              label: "In Channel Agent",
              type: "agent",
              handle: "agent_in",
              secondaryLabel: "@agent_in",
              group: "in_channel",
            },
          ]}
          query=""
          command={command}
        />
      </I18nWrapper>,
    );

    expect(screen.getByText("In this channel")).toBeInTheDocument();
    expect(screen.getByText("Not in this channel")).toBeInTheDocument();

    fireEvent.click(screen.getByText("In Channel Agent").closest("button")!);
    expect(command).toHaveBeenCalledTimes(1);
    expect(command).toHaveBeenCalledWith(
      expect.objectContaining({ id: "a-in", group: "in_channel" }),
    );

    command.mockClear();
    // Enter selects the highlighted row (index 0 = first visible = in-channel)
    ref.current?.onKeyDown({ event: new KeyboardEvent("keydown", { key: "Enter" }) });
    expect(command).toHaveBeenCalledWith(
      expect.objectContaining({ id: "a-in", group: "in_channel" }),
    );
  });

  it("keeps in-channel actors and pages outsiders from mention-candidates", async () => {
    const fetchMentionCandidates = vi.fn(async (_query: string, offset: number) => {
      if (offset > 0) {
        return {
          in_channel: [
            { id: "li-wei", label: "里维", type: "agent" as const, handle: "li-wei", group: "in_channel" as const },
          ],
          not_in_channel: [
            { id: "a-out-20", label: "Agent 20", type: "agent" as const, handle: "agent-20", group: "not_in_channel" as const },
          ],
          has_more: false,
          next_offset: null,
        };
      }
      return {
        in_channel: [
          { id: "li-wei", label: "里维", type: "agent" as const, handle: "li-wei", group: "in_channel" as const },
          { id: "a-tai", label: "阿泰", type: "agent" as const, handle: "a-tai", group: "in_channel" as const },
        ],
        not_in_channel: [
          { id: "a-out-0", label: "Agent 00", type: "agent" as const, handle: "agent-0", group: "not_in_channel" as const },
        ],
        has_more: true,
        next_offset: 20,
      };
    });

    render(
      <I18nWrapper>
        <MentionList
          items={[
            { id: "li-wei", label: "里维", type: "agent", handle: "li-wei", group: "in_channel" },
          ]}
          query=""
          command={() => {}}
          fetchMentionCandidates={fetchMentionCandidates}
        />
      </I18nWrapper>,
    );

    expect(await screen.findByText("里维")).toBeInTheDocument();
    expect(screen.getByText("阿泰")).toBeInTheDocument();
    expect(screen.getByText("Agent 00")).toBeInTheDocument();
    expect(screen.queryByText("Agent 20")).toBeNull();
    expect(fetchMentionCandidates).toHaveBeenCalledWith("", 0, expect.any(AbortSignal));
  });

  it("does not dump workspace outsiders into the sync pool when mention-candidates is active", () => {
    const qc = fakeQc({
      members: [
        { user_id: "u-in", name: "in-channel", role: "member" },
        { user_id: "u-out", name: "not-in-channel", role: "member" },
      ],
      agents: [
        {
          id: "a-in",
          name: "li-wei",
          display_name: "里维",
          archived_at: null,
          visibility: "workspace",
          owner_id: null,
        },
        {
          id: "a-out",
          name: "agentpro",
          display_name: "AgentPro",
          archived_at: null,
          visibility: "workspace",
          owner_id: null,
        },
      ],
    });
    const config = createMentionSuggestion(qc, {
      getAllowedActorIds: () => new Set(["u-in", "u-out", "a-in", "a-out"]),
      getChannelMemberIds: () => new Set(["u-in", "a-in"]),
      getMentionCandidates: () => async () => ({
        in_channel: [],
        not_in_channel: [],
        has_more: false,
        next_offset: null,
      }),
    });
    const items = config.items!({ query: "", editor: {} as never }) as MentionItem[];
    expect(items.map((item) => item.id).sort()).toEqual(["a-in", "u-in"]);
  });
});
