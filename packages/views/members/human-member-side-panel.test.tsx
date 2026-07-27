import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { HumanMemberSidePanel } from "./human-member-side-panel";

const memberProfileMock = vi.fn();
const membersMock = vi.fn();
const agentsMock = vi.fn();
const runCountsMock = vi.fn();
const runtimesMock = vi.fn();

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } | null; setUser: () => void }) => unknown) =>
    sel({ user: { id: "user-self" }, setUser: vi.fn() }),
}));

vi.mock("@multica/core/agents", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/agents")>();
  return {
    ...actual,
    memberProfileOptions: () => ({
      queryKey: ["member-profile"],
      queryFn: () => memberProfileMock(),
    }),
    agentRunCounts30dOptions: () => ({
      queryKey: ["run-counts"],
      queryFn: () => runCountsMock(),
    }),
  };
});

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({
    queryKey: ["members"],
    queryFn: () => membersMock(),
  }),
  agentListOptions: () => ({
    queryKey: ["agents"],
    queryFn: () => agentsMock(),
  }),
  workspaceKeys: {
    memberProfile: () => ["member-profile"],
  },
}));

vi.mock("@multica/core/runtimes/queries", () => ({
  runtimeListOptions: () => ({
    queryKey: ["runtimes"],
    queryFn: () => runtimesMock(),
  }),
}));

vi.mock("../common/use-open-dm", () => ({
  useOpenDM: () => ({ openDM: vi.fn(), isPending: false }),
}));

vi.mock("../common/agent-panel-context", () => ({
  useOpenAgentPanel: () => null,
}));

vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (sel: (s: { open: () => void }) => unknown) =>
    sel({ open: vi.fn() }),
}));

vi.mock("../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (keys: Record<string, unknown>) => string) =>
      fn({
        role: { owner: "Owner", admin: "Admin", member: "Member" },
        side_panel: {
          description: "Description",
          description_placeholder: "Add a description",
          no_description: "No description",
          info: "Info",
          role: "Role",
          email: "Email",
          joined: "Joined",
          created_agents: "Created Agents",
          no_agents: "No agents created yet",
          you_suffix: "(you)",
          message_aria: "Send message",
          close_aria: "Close profile",
          unavailable: "Member unavailable",
          unknown: "Unknown",
          resize_aria: "Resize",
        },
      } as never),
  }),
}));

vi.mock("../agents/components/inline-field-editor", () => ({
  InlineFieldEditor: ({ emptyLabel, value }: { emptyLabel?: string; value: string }) => (
    <div data-testid="human-member-description-editor-trigger">
      {value || emptyLabel}
    </div>
  ),
}));

vi.mock("../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="actor-avatar" />,
}));

vi.mock("@multica/ui/components/common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="base-avatar" />,
}));

function renderPanel(userId = "user-self") {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <HumanMemberSidePanel userId={userId} onClose={vi.fn()} />
    </QueryClientProvider>,
  );
}

describe("HumanMemberSidePanel (LRM-619 Lock A)", () => {
  beforeEach(() => {
    memberProfileMock.mockReset();
    membersMock.mockReset();
    agentsMock.mockReset();
    runCountsMock.mockReset();
    runtimesMock.mockReset();

    memberProfileMock.mockResolvedValue({
      member_type: "user",
      member_id: "user-self",
      name: "frank-an",
      display_name: "Frank An",
      avatar_url: null,
      description: "",
      role: "owner",
      status: null,
      recent_activity: [],
      profile_access: "full",
    });
    membersMock.mockResolvedValue([
      {
        id: "m1",
        workspace_id: "ws-1",
        user_id: "user-self",
        role: "owner",
        created_at: "2026-06-11T00:00:00Z",
        name: "frank-an",
        display_name: "Frank An",
        email: "me.frankan@gmail.com",
        avatar_url: null,
        profile_description: "",
      },
    ]);
    agentsMock.mockResolvedValue([
      {
        id: "agent-1",
        name: "cindy",
        display_name: "Cindy",
        description: "HR",
        owner_id: "user-self",
        runtime_id: "rt-1",
        runtime_mode: "local",
        avatar_url: null,
        archived_at: null,
      },
    ]);
    runCountsMock.mockResolvedValue([{ agent_id: "agent-1", run_count: 3 }]);
    runtimesMock.mockResolvedValue([
      { id: "rt-1", name: "dev", provider: "claude" },
    ]);
  });

  it("renders five Lock A sections with explicit empty description", async () => {
    renderPanel();
    expect(await screen.findByTestId("human-member-topbar")).toBeInTheDocument();
    expect(screen.getByTestId("human-member-identity")).toBeInTheDocument();
    expect(screen.getByTestId("human-member-description")).toBeInTheDocument();
    expect(screen.getByText("No description")).toBeInTheDocument();
    expect(screen.getByTestId("human-member-info")).toBeInTheDocument();
    expect(screen.getByTestId("human-member-role-pill")).toHaveTextContent("Owner");
    expect(screen.getByText("me.frankan@gmail.com")).toBeInTheDocument();
    expect(screen.getByTestId("human-member-created-agents")).toBeInTheDocument();
    expect(screen.getByText("Cindy - HR")).toBeInTheDocument();
    expect(screen.getByText("Claude Code")).toBeInTheDocument();
  });

  it("hides email when member list email is empty (no silent fake)", async () => {
    membersMock.mockResolvedValue([
      {
        id: "m1",
        workspace_id: "ws-1",
        user_id: "user-self",
        role: "owner",
        created_at: "2026-06-11T00:00:00Z",
        name: "frank-an",
        display_name: "Frank An",
        email: "",
        avatar_url: null,
        profile_description: "",
      },
    ]);
    renderPanel();
    expect(await screen.findByTestId("human-member-role-pill")).toBeInTheDocument();
    expect(screen.queryByText("Email")).not.toBeInTheDocument();
    expect(screen.queryByText("me.frankan@gmail.com")).not.toBeInTheDocument();
  });

  it("shows explicit empty agents state", async () => {
    agentsMock.mockResolvedValue([]);
    renderPanel();
    expect(await screen.findByText("No agents created yet")).toBeInTheDocument();
  });
});
