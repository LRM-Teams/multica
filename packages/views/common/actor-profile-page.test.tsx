// @vitest-environment jsdom
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { ActorProfilePage } from "./actor-profile-page";

const mockBack = vi.hoisted(() => vi.fn());
const mockReplace = vi.hoisted(() => vi.fn());
const { members, currentUser } = vi.hoisted(() => ({
  members: [],
  currentUser: { id: "user-owner" } as { id: string } | null,
}));

// PageHeader pulls in the sidebar context; stub it to a passthrough so the page
// test stays focused on the back button + content wiring. `className` is kept so
// the safe-area assertion (LRM-1185) sees what the page actually passes.
vi.mock("../layout/page-header", () => ({
  PageHeader: ({
    children,
    className,
  }: {
    children: React.ReactNode;
    className?: string;
  }) => (
    <div data-testid="page-header" className={className}>
      {children}
    </div>
  ),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ back: mockBack, replace: mockReplace, push: vi.fn() }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: members }),
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ channels: () => "/acme/channels" }),
}));
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
    onClose,
  }: {
    userId: string;
    variant?: string;
    onClose: () => void;
  }) => (
    <div data-testid="member-side-panel-page" data-user-id={userId}>
      {variant}
      <button
        type="button"
        data-testid="member-panel-close-proxy"
        onClick={onClose}
      />
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
  // jsdom reports `history.length === 1` (a fresh tab), which the LRM-1185 exit
  // treats as "nothing to pop". Default these specs to "arrived from a channel"
  // and let the deep-link spec opt into the empty-history branch.
  const setHistoryLength = (value: number) => {
    Object.defineProperty(window.history, "length", {
      configurable: true,
      value,
    });
  };

  beforeEach(() => {
    mockBack.mockClear();
    mockReplace.mockClear();
    setHistoryLength(2);
  });
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

  describe("narrow-screen dismiss chrome (LRM-1185, 父 LRM-974 冻 A1)", () => {

    it("gives the leading ← a 44x44 hit target with a 20px glyph and readable label", () => {
      render(<ActorProfilePage memberType="agent" memberId="a1" />);

      const back = screen.getByTestId("actor-profile-back");
      expect(back).toHaveClass("h-11", "min-w-11");
      expect(back).toHaveClass("text-foreground");
      expect(back).not.toHaveClass("h-7");
      expect(back.querySelector("svg")).toHaveClass("size-5");
      expect(back).toHaveAttribute("aria-label", "Back");
    });

    it("keeps the chrome out of the notch via safe-area-inset-top", () => {
      render(<ActorProfilePage memberType="agent" memberId="a1" />);

      expect(screen.getByTestId("page-header").className).toContain(
        "pt-[env(safe-area-inset-top,0px)]",
      );
    });

    it("falls back to the channel list when there is no history entry to pop", () => {
      setHistoryLength(1);

      render(<ActorProfilePage memberType="agent" memberId="a1" />);
      fireEvent.click(screen.getByTestId("actor-profile-back"));

      expect(mockBack).not.toHaveBeenCalled();
      expect(mockReplace).toHaveBeenCalledWith("/acme/channels");
    });

    it("routes the panel's own close control through the same exit", () => {
      render(<ActorProfilePage memberType="user" memberId="u1" />);

      fireEvent.click(screen.getByTestId("member-panel-close-proxy"));
      expect(mockBack).toHaveBeenCalled();
    });
  });
});
