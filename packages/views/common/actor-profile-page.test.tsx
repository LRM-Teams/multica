// @vitest-environment jsdom
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ActorProfilePage } from "./actor-profile-page";

const mockBack = vi.hoisted(() => vi.fn());
const { agents, members, currentUser } = vi.hoisted(() => ({
  agents: [] as Array<{ id: string }>,
  members: [],
  currentUser: { id: "user-owner" } as { id: string } | null,
}));

// PageHeader pulls in the sidebar context; stub it to a passthrough so the page
// test stays focused on the back button + content wiring.
vi.mock("../layout/page-header", () => ({
  PageHeader: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="page-header">{children}</div>
  ),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ back: mockBack, push: vi.fn() }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey: string[] }) => ({
    data: options.queryKey.includes("agents") ? agents : members,
  }),
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/auth", () => ({ useAuthStore: () => currentUser }));
vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["agents"] }),
  memberListOptions: () => ({ queryKey: ["members"] }),
}));

vi.mock("./resolved-agent-side-panel", () => ({
  ResolvedAgentSidePanel: ({
    variant,
    agentId,
  }: {
    variant: string;
    agentId: string;
  }) => (
    <div data-testid="agent-tabs" data-agent-id={agentId}>
      {variant}
    </div>
  ),
}));

vi.mock("../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: { profile_popover: { back: string } }) => string) =>
      selector({ profile_popover: { back: "Back" } }),
  }),
}));

// Users retain the generic profile fallback. Stub it to echo props so we can
// assert that fallback forwards memberType/memberId.
vi.mock("./actor-profile-popover", () => ({
  ActorProfileContent: ({
    memberType,
    memberId,
  }: {
    memberType: string;
    memberId: string;
  }) => (
    <div data-testid="actor-profile-content">
      {memberType}:{memberId}
    </div>
  ),
}));

describe("ActorProfilePage (#586 mobile full page)", () => {
  it("resolves agents by id even when absent from ListAgents (LRM-288)", () => {
    agents.splice(0, agents.length);
    render(<ActorProfilePage memberType="agent" memberId="group-manager-1" />);

    const agentTabs = screen.getByTestId("agent-tabs");
    expect(agentTabs).toHaveAttribute("data-agent-id", "group-manager-1");
    expect(agentTabs).toHaveTextContent("page");
  });

  it("reuses the agent tab surface for agents", () => {
    agents.splice(0, agents.length, { id: "agent-1" });
    render(<ActorProfilePage memberType="agent" memberId="agent-1" />);

    const agentTabs = screen.getByTestId("agent-tabs");
    expect(agentTabs).toHaveTextContent("page");
    // The page route must pass its bounded height through to AgentSidePanel so
    // the panel's existing tab body owns scrolling. If this wrapper becomes an
    // outer scroller again, the tab bar disappears while reading old Activity.
    expect(agentTabs.parentElement).toHaveClass("flex", "min-h-0", "flex-1");
    expect(agentTabs.parentElement?.parentElement).toHaveClass("flex", "min-h-0", "flex-1");
    expect(agentTabs.parentElement?.parentElement).not.toHaveClass("overflow-y-auto");
    agents.splice(0, agents.length);
  });

  it("keeps the generic profile fallback as the page scroll owner", () => {
    render(<ActorProfilePage memberType="user" memberId="u1" />);

    expect(screen.getByTestId("actor-profile-content").parentElement?.parentElement).toHaveClass(
      "overflow-y-auto",
    );
  });

  it("renders a Back button that calls navigation.back()", () => {
    render(<ActorProfilePage memberType="user" memberId="u1" />);

    const back = screen.getByRole("button", { name: /back/i });
    fireEvent.click(back);
    expect(mockBack).toHaveBeenCalled();
  });
});
