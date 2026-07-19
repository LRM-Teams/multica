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

vi.mock("../channels/components/agent-side-panel", () => ({
  AgentSidePanel: ({ variant }: { variant: string }) => <div data-testid="agent-tabs">{variant}</div>,
}));

vi.mock("../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: { profile_popover: { back: string } }) => string) =>
      selector({ profile_popover: { back: "Back" } }),
  }),
}));

// Users and unavailable agents retain the generic profile fallback. Stub it to
// echo props so we can assert that fallback forwards memberType/memberId.
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
  it("renders the shared profile content for the actor", () => {
    render(<ActorProfilePage memberType="agent" memberId="agent-1" />);

    expect(screen.getByTestId("actor-profile-content")).toHaveTextContent(
      "agent:agent-1",
    );
  });

  it("reuses the agent tab surface when the agent is available", () => {
    agents.splice(0, agents.length, { id: "agent-1" });
    render(<ActorProfilePage memberType="agent" memberId="agent-1" />);

    expect(screen.getByTestId("agent-tabs")).toHaveTextContent("page");
    agents.splice(0, agents.length);
  });

  it("renders a Back button that calls navigation.back()", () => {
    render(<ActorProfilePage memberType="user" memberId="u1" />);

    const back = screen.getByRole("button", { name: /back/i });
    fireEvent.click(back);
    expect(mockBack).toHaveBeenCalledTimes(1);
  });
});
