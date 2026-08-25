import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, AgentRuntime, MemberWithUser } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { MembersDirectoryPage } from "./members-directory-page";

const nav = vi.hoisted(() => ({
  search: "",
  listeners: new Set<() => void>(),
  go(href: string) {
    this.search = href.split("?")[1] ?? "";
    for (const l of this.listeners) l();
  },
}));

const data = vi.hoisted(() => ({
  agents: [] as unknown[],
  members: [] as unknown[],
  runtimes: [] as unknown[],
  presence: new Map<string, string>(),
}));

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useQuery: (options: { queryKey?: unknown[] }) => {
      const key = String(options.queryKey ?? []);
      if (key.includes("agents")) {
        return { data: data.agents, isLoading: false };
      }
      if (key.includes("members")) {
        return { data: data.members, isLoading: false };
      }
      if (key.includes("runtimes")) {
        return { data: data.runtimes, isLoading: false };
      }
      return { data: undefined, isLoading: false };
    },
  };
});

vi.mock("@multica/core/api", () => ({ api: {} }));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } }) => unknown) =>
    sel({ user: { id: "u-self" } }),
}));

vi.mock("@multica/core/agents", () => ({
  useWorkspaceAgentPresence: () => ({
    byAgent: data.presence,
    loading: false,
  }),
}));

vi.mock("@multica/core/paths", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/paths")>(
    "@multica/core/paths",
  );
  return {
    ...actual,
    useWorkspacePaths: () => ({
      members: (selection?: { kind: string; id: string }) =>
        selection
          ? actual.membersPathWithSelection(
              "/ws-1/members",
              selection.kind as "agent" | "user",
              selection.id,
            )
          : "/ws-1/members",
    }),
  };
});

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["workspaces", "ws-1", "agents"] }),
  memberListOptions: () => ({ queryKey: ["workspaces", "ws-1", "members"] }),
  workspaceKeys: {
    agents: () => ["workspaces", "ws-1", "agents"],
    members: () => ["workspaces", "ws-1", "members"],
  },
}));

vi.mock("@multica/core/runtimes", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/runtimes")>(
    "@multica/core/runtimes",
  );
  return {
    ...actual,
    runtimeListOptions: () => ({ queryKey: ["workspaces", "ws-1", "runtimes"] }),
  };
});

vi.mock("../navigation", async () => {
  const React = await import("react");
  return {
    useNavigation: () => {
      const [, force] = React.useReducer((x: number) => x + 1, 0);
      React.useEffect(() => {
        nav.listeners.add(force);
        return () => {
          nav.listeners.delete(force);
        };
      }, []);
      return {
        replace: (href: string) => nav.go(href),
        searchParams: new URLSearchParams(nav.search),
      };
    },
  };
});

vi.mock("../common/actor-avatar", () => ({
  ActorAvatar: ({
    actorId,
    showStatusDot,
    agentPresence,
  }: {
    actorId: string;
    showStatusDot?: boolean;
    agentPresence?: string;
  }) => (
    <div
      data-testid={`avatar-${actorId}`}
      data-status-dot={showStatusDot ? "on" : "off"}
      data-presence={agentPresence ?? ""}
    />
  ),
}));

vi.mock("../common/resolved-agent-side-panel", () => ({
  ResolvedAgentSidePanel: ({ agentId }: { agentId: string }) => (
    <div data-testid={`agent-panel-${agentId}`} />
  ),
}));

vi.mock("./member-side-panel", () => ({
  MemberSidePanel: ({ userId }: { userId: string }) => (
    <div data-testid={`member-panel-${userId}`} />
  ),
}));

vi.mock("./invite-human-dialog", () => ({
  InviteHumanDialog: () => null,
}));

vi.mock("../agents/components/create-agent-dialog", () => ({
  CreateAgentDialog: () => null,
}));

function agent(id: string, name: string, extra: Partial<Agent> = {}): Agent {
  return {
    id,
    name,
    workspace_id: "ws-1",
    runtime_id: "rt-1",
    owner_id: "u-self",
    status: "idle",
    description: null,
    instructions: "",
    avatar_url: null,
    display_name: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    ...extra,
  } as Agent;
}

function member(
  userId: string,
  name: string,
  role: string = "member",
): MemberWithUser {
  return {
    id: `m-${userId}`,
    user_id: userId,
    workspace_id: "ws-1",
    role,
    name,
    email: `${name.toLowerCase()}@example.com`,
    avatar_url: null,
    created_at: "2026-01-01T00:00:00Z",
  } as unknown as MemberWithUser;
}

const runtime: AgentRuntime = {
  id: "rt-1",
  name: "Pi",
  workspace_id: "ws-1",
  owner_id: "u-self",
  runtime_mode: "local",
  status: "online",
  daemon_id: "d1",
  device_name: "s144",
  last_seen_at: "2026-01-01T00:00:00Z",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
} as unknown as AgentRuntime;

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <MembersDirectoryPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  nav.search = "";
  nav.listeners.clear();
  data.agents = [agent("a1", "Alice"), agent("a2", "Bob")];
  data.members = [
    member("u-self", "Frank", "owner"),
    member("u2", "Joyce", "admin"),
    member("u3", "Kim", "member"),
  ];
  data.runtimes = [runtime];
  data.presence = new Map([["a1", "online"]]);
});

describe("MembersDirectoryPage rail", () => {
  it("shows a presence dot on every rail avatar, agents fed by one snapshot", async () => {
    renderPage();
    await screen.findByTestId("members-agent-row-a1");

    expect(screen.getByTestId("avatar-a1")).toHaveAttribute(
      "data-status-dot",
      "on",
    );
    expect(screen.getByTestId("avatar-a1")).toHaveAttribute(
      "data-presence",
      "online",
    );
    // Missing from the snapshot reads as offline, never a per-row query.
    expect(screen.getByTestId("avatar-a2")).toHaveAttribute(
      "data-presence",
      "offline",
    );
    expect(screen.getByTestId("avatar-u2")).toHaveAttribute(
      "data-status-dot",
      "on",
    );
  });

  it("badges owner/admin humans and leaves plain members unbadged", async () => {
    renderPage();
    await screen.findByTestId("members-human-row-u2");

    expect(screen.getByTestId("members-role-badge-u-self")).toHaveTextContent(
      "Owner",
    );
    expect(screen.getByTestId("members-role-badge-u2")).toHaveTextContent(
      "Admin",
    );
    expect(
      screen.queryByTestId("members-role-badge-u3"),
    ).not.toBeInTheDocument();
  });

  it("distinguishes a no-match search from an empty section", async () => {
    const user = userEvent.setup();
    renderPage();
    const search = await screen.findByTestId("members-directory-search");

    await user.type(search, "zzz");
    await waitFor(() =>
      expect(screen.getByTestId("members-directory-no-results")).toBeVisible(),
    );
    expect(screen.queryByText("No humans yet")).not.toBeInTheDocument();
  });

  it("clears the search from the inline clear button", async () => {
    const user = userEvent.setup();
    renderPage();
    const search = await screen.findByTestId("members-directory-search");

    await user.type(search, "joyce");
    await waitFor(() =>
      expect(
        screen.queryByTestId("members-human-row-u3"),
      ).not.toBeInTheDocument(),
    );

    await user.click(screen.getByTestId("members-directory-search-clear"));
    await screen.findByTestId("members-human-row-u3");
    expect(search).toHaveValue("");
  });

  it("moves the selection with ArrowDown/ArrowUp across sections", async () => {
    const user = userEvent.setup();
    nav.search = "member=agent%3Aa1";
    renderPage();
    await screen.findByTestId("agent-panel-a1");

    const search = screen.getByTestId("members-directory-search");
    search.focus();

    await user.keyboard("{ArrowDown}");
    await screen.findByTestId("agent-panel-a2");

    // Past the last agent lands on the first human — one continuous list.
    await user.keyboard("{ArrowDown}");
    await screen.findByTestId("member-panel-u-self");

    await user.keyboard("{ArrowUp}");
    await screen.findByTestId("agent-panel-a2");
  });

  it("Escape in the rail clears an active search", async () => {
    const user = userEvent.setup();
    renderPage();
    const search = await screen.findByTestId("members-directory-search");

    await user.type(search, "joyce");
    await waitFor(() => expect(search).toHaveValue("joyce"));

    await user.keyboard("{Escape}");
    await waitFor(() => expect(search).toHaveValue(""));
  });
});
