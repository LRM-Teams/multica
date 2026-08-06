// @vitest-environment jsdom
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ActorProfilePage } from "./actor-profile-page";

const mockBack = vi.hoisted(() => vi.fn());
const { members, currentUser } = vi.hoisted(() => ({
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
  useQuery: () => ({ data: members }),
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/auth", () => ({ useAuthStore: () => currentUser }));
vi.mock("@multica/core/workspace/queries", () => ({
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

vi.mock("../members/member-side-panel", () => ({
  MemberSidePanel: ({
    userId,
    variant,
  }: {
    userId: string;
    variant?: string;
  }) => (
    <div data-testid="member-side-panel-page" data-user-id={userId}>
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

describe("ActorProfilePage (#586 mobile full page)", () => {
  it("opens agents by id via ResolvedAgentSidePanel (LRM-292, no ListAgents gate)", () => {
    render(<ActorProfilePage memberType="agent" memberId="group-manager-1" />);

    const agentTabs = screen.getByTestId("agent-tabs");
    expect(agentTabs).toHaveAttribute("data-agent-id", "group-manager-1");
    expect(agentTabs).toHaveTextContent("page");
  });

  it("reuses the agent tab surface for agents", () => {
    render(<ActorProfilePage memberType="agent" memberId="agent-1" />);

    const agentTabs = screen.getByTestId("agent-tabs");
    expect(agentTabs).toHaveTextContent("page");
    // The page route must pass its bounded height through to AgentSidePanel so
    // the panel's existing tab body owns scrolling. If this wrapper becomes an
    // outer scroller again, the tab bar disappears while reading old Activity.
    expect(agentTabs.parentElement).toHaveClass("flex", "min-h-0", "flex-1");
    expect(agentTabs.parentElement?.parentElement).toHaveClass("flex", "min-h-0", "flex-1");
    expect(agentTabs.parentElement?.parentElement).not.toHaveClass("overflow-y-auto");
  });

  it("renders human member Profile page via MemberSidePanel (LRM-619)", () => {
    render(<ActorProfilePage memberType="user" memberId="u1" />);

    const panel = screen.getByTestId("member-side-panel-page");
    expect(panel).toHaveAttribute("data-user-id", "u1");
    expect(panel).toHaveTextContent("page");
    expect(panel.parentElement).toHaveClass("flex", "min-h-0", "flex-1");
    expect(panel.parentElement?.parentElement).not.toHaveClass("overflow-y-auto");
  });

  it("renders a Back button that calls navigation.back()", () => {
    render(<ActorProfilePage memberType="user" memberId="u1" />);

    const back = screen.getByRole("button", { name: /back/i });
    fireEvent.click(back);
    expect(mockBack).toHaveBeenCalled();
  });
});
