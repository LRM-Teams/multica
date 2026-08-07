import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ActorAvatar, AgentPresenceOverlay, AgentStatusDot } from "./actor-avatar";

// AgentStatusDot reads presence via useAgentPresenceDetail and the current
// workspace via useCurrentWorkspace. Default to "online + idle" so the dot
// renders; individual tests override the availability/workload per case.
type PresenceDetail = {
  availability: "online" | "offline";
  workload: "idle" | "working" | "queued";
  runningCount: number;
  queuedCount: number;
  capacity: number;
};
const presenceDetailMock = vi.fn((): PresenceDetail => ({
  availability: "online",
  workload: "idle",
  runningCount: 0,
  queuedCount: 0,
  capacity: 1,
}));

const runnerActivityMock = vi.fn((): {
  data: { summary: { label: string; tone: string; visibility: string }; timeline: never[] };
} => ({
  data: { summary: { label: "Online", tone: "success", visibility: "visible" }, timeline: [] },
}));

vi.mock("@multica/core/agents", () => ({
  useAgentPresenceDetail: () => presenceDetailMock(),
  useRunnerActivity: () => runnerActivityMock(),
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1" }),
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/agents/${id}`,
    memberDetail: (id: string) => `/members/${id}`,
  }),
}));

// Panel-click behavior (#349 app-wide): agent single-click opens the side
// panel; ⌘/ctrl/shift-click routes to the full detail page; a control-ancestor
// (row link/button) defers to that outer interactive.
const openFromStoreMock = vi.fn<(id: string) => void>();
const openFromContextMock = vi.fn<(id: string) => void>();
const openInNewTabMock = vi.fn();
let contextAvailable = false;

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: () => "Agent One",
    getActorInitials: () => "A1",
    getActorAvatarUrl: () => undefined,
  }),
}));

vi.mock("./use-resolved-actor-identity", () => ({
  mentionTypeFromActorType: (type: string) =>
    type === "agent" ? "agent" : type === "member" ? "member" : null,
  useResolvedActorIdentity: () => ({
    displayName: "Agent One",
    avatarUrl: null,
  }),
  resolvedActorLabel: (
    identity: { displayName: string | null },
    actorId: string,
  ) => identity.displayName ?? actorId,
}));

const openMemberFromStoreMock = vi.fn<(id: string) => void>();
const closePanelMock = vi.fn();

vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (
    selector: (s: { open: (id: string) => void; close: () => void }) => unknown,
  ) => selector({ open: openFromStoreMock, close: closePanelMock }),
  useAgentXpBurstStore: (selector: (s: { bursts: Record<string, never> }) => unknown) =>
    selector({ bursts: {} }),
}));

vi.mock("@multica/core/workspace", () => ({
  useMemberPanelStore: (
    selector: (s: {
      open: (id: string) => void;
      close: () => void;
      selectedUserId: string | null;
    }) => unknown,
  ) =>
    selector({
      open: openMemberFromStoreMock,
      close: closePanelMock,
      selectedUserId: null,
    }),
}));

vi.mock("./agent-panel-context", () => ({
  useOpenAgentPanel: () => (contextAvailable ? openFromContextMock : null),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ openInNewTab: openInNewTabMock }),
}));

vi.mock("./actor-profile-popover", () => ({
  ActorProfileContent: ({
    memberType,
    memberId,
  }: {
    memberType: string;
    memberId: string;
  }) => (
    <div
      data-testid="actor-profile-content"
      data-member-type={memberType}
      data-member-id={memberId}
    />
  ),
}));

vi.mock("@multica/ui/components/ui/hover-card", () => ({
  HoverCard: ({ children }: { children: ReactNode }) => (
    <div data-testid="hover-card">{children}</div>
  ),
  HoverCardTrigger: ({
    children,
    ...props
  }: {
    children: ReactNode;
    render?: ReactElement;
    className?: string;
    tabIndex?: number;
  }) => (
    <span data-testid="hover-card-trigger" className={props.className} tabIndex={props.tabIndex}>
      {children}
    </span>
  ),
  HoverCardContent: ({ children }: { children: ReactNode }) => (
    <div data-testid="hover-card-content">{children}</div>
  ),
}));

describe("AgentPresenceOverlay", () => {
  beforeEach(() => {
    presenceDetailMock.mockReturnValue({
      availability: "online",
      workload: "idle",
      runningCount: 0,
      queuedCount: 0,
      capacity: 1,
    });
  });

  // The bug: inside a CSS grid / flex parent with the default
  // `align-items: stretch`, a bare `relative inline-flex` presence wrapper
  // stretches to the row height, so `absolute bottom-0 right-0` anchors to the
  // row's bottom instead of the avatar. The overlay must be a fixed-size,
  // non-stretchable box that hugs the avatar.
  it("wraps the avatar in a fixed-size, non-stretchable box even inside a tall stretch parent", () => {
    render(
      <div style={{ display: "grid", alignItems: "stretch", height: 240 }}>
        <AgentPresenceOverlay agentId="agent-1" size={28}>
          <div data-testid="avatar" style={{ width: 28, height: 28 }} />
        </AgentPresenceOverlay>
      </div>,
    );

    const dot = screen.getByLabelText(/^Status:/);
    const box = dot.closest('[data-slot="agent-presence"]');
    expect(box).not.toBeNull();
    // Fixed-size + shrink-0 => immune to align-items: stretch (which only
    // stretches items whose cross size is auto). The dot lives inside this box.
    expect(box).toHaveClass("shrink-0");
    expect(box).toHaveStyle({ width: "28px", height: "28px" });
    expect(box).toContainElement(dot);
    expect(box).toContainElement(screen.getByTestId("avatar"));
  });

  it("defaults the box to the base avatar size (20px) when size is omitted", () => {
    render(
      <AgentPresenceOverlay agentId="agent-1">
        <div style={{ width: 20, height: 20 }} />
      </AgentPresenceOverlay>,
    );
    const box = screen.getByLabelText(/^Status:/).closest('[data-slot="agent-presence"]');
    expect(box).toHaveStyle({ width: "20px", height: "20px" });
  });

  it("merges an extra className (e.g. a baseline nudge) onto the box", () => {
    render(
      <AgentPresenceOverlay agentId="agent-1" size={28} className="mt-0.5">
        <div />
      </AgentPresenceOverlay>,
    );
    const box = screen.getByLabelText(/^Status:/).closest('[data-slot="agent-presence"]');
    expect(box).toHaveClass("mt-0.5");
    expect(box).toHaveClass("relative");
  });

  it("LRM-1119: presence box opts out of overflow clipping so the corner dot can paint", () => {
    render(
      <AgentPresenceOverlay agentId="agent-1" size={40}>
        <div />
      </AgentPresenceOverlay>,
    );
    const box = screen.getByLabelText(/^Status:/).closest('[data-slot="agent-presence"]');
    expect(box).toHaveClass("overflow-visible");
  });

});

describe("AgentStatusDot", () => {
  beforeEach(() => {
    presenceDetailMock.mockReturnValue({
      availability: "online",
      workload: "idle",
      runningCount: 0,
      queuedCount: 0,
      capacity: 1,
    });
    runnerActivityMock.mockReturnValue({
      data: { summary: { label: "Online", tone: "success", visibility: "visible" }, timeline: [] },
    });
  });

  it("scales the dot diameter with the avatar size, clamped to a legible minimum", () => {
    const { rerender } = render(<AgentStatusDot agentId="agent-1" size={40} />);
    // 40 * 0.28 ≈ 11px on a large avatar.
    expect(screen.getByLabelText(/^Status:/)).toHaveStyle({ width: "11px", height: "11px" });

    // A tiny 14px stack avatar clamps to the 5px floor rather than vanishing.
    rerender(<AgentStatusDot agentId="agent-1" size={14} />);
    expect(screen.getByLabelText(/^Status:/)).toHaveStyle({ width: "5px", height: "5px" });
  });

  it("carries a surface-colored cut-out ring so it reads on any background", () => {
    render(<AgentStatusDot agentId="agent-1" size={28} />);
    const dot = screen.getByLabelText(/^Status:/);
    expect(dot).toHaveClass("ring-2");
    expect(dot).toHaveClass("ring-background");
  });

  it("LRM-1119: insets the corner anchor by ring width so fill+ring stay inside the box", () => {
    render(<AgentStatusDot agentId="agent-1" size={40} />);
    const anchor = screen.getByLabelText(/^Status:/).parentElement;
    expect(anchor).toHaveClass("bottom-0.5");
    expect(anchor).toHaveClass("right-0.5");
  });

  it("uses the success color when online but never fakes green when offline", () => {
    const { rerender } = render(<AgentStatusDot agentId="agent-1" size={28} />);
    expect(screen.getByLabelText(/^Status:/)).toHaveClass("bg-success");

    presenceDetailMock.mockReturnValue({
      availability: "offline",
      workload: "idle",
      runningCount: 0,
      queuedCount: 0,
      capacity: 1,
    });
    rerender(<AgentStatusDot agentId="agent-1" size={28} />);
    const dot = screen.getByLabelText(/^Status:/);
    expect(dot).not.toHaveClass("bg-success");
    expect(dot).toHaveClass("border-muted-foreground/50");
    expect(dot).toHaveClass("bg-transparent");
  });

  it("renders nothing while presence is still loading", () => {
    presenceDetailMock.mockReturnValue("loading" as never);
    const { container } = render(<AgentStatusDot agentId="agent-1" size={28} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows a yellow pulse for an online Runner working on chat", () => {
    presenceDetailMock.mockReturnValue({
      availability: "online",
      workload: "idle",
      runningCount: 0,
      queuedCount: 0,
      capacity: 1,
    });
    runnerActivityMock.mockReturnValue({
      data: { summary: { label: "Running command...", tone: "warning", visibility: "visible" }, timeline: [] },
    });
    const { container, rerender } = render(<AgentStatusDot agentId="agent-1" size={28} />);
    expect(container.querySelector(".animate-ping")).not.toBeNull();
    expect(screen.getByLabelText(/^Status:/)).toHaveClass("bg-warning");

    presenceDetailMock.mockReturnValue({
      availability: "offline",
      workload: "idle",
      runningCount: 0,
      queuedCount: 0,
      capacity: 1,
    });
    rerender(<AgentStatusDot agentId="agent-1" size={28} />);
    expect(container.querySelector(".animate-ping")).toBeNull();
  });

  it("renders an OFFLINE dot as a hollow ring at legible sizes, filled on tiny ones (§3-v2)", () => {
    presenceDetailMock.mockReturnValue({
      availability: "offline",
      workload: "idle",
      runningCount: 0,
      queuedCount: 0,
      capacity: 1,
    });
    // Legible size (40 → ~11px dot) → hollow ring, no filled gray.
    const { rerender } = render(<AgentStatusDot agentId="agent-1" size={40} />);
    let dot = screen.getByLabelText(/^Status:/);
    expect(dot).toHaveClass("border-2");
    expect(dot).toHaveClass("bg-transparent");
    expect(dot).not.toHaveClass("bg-muted-foreground/40");

    // Tiny participant-stack dot (14 → clamped to 5px) → hollow unreadable, so
    // it falls back to the filled gray.
    rerender(<AgentStatusDot agentId="agent-1" size={14} />);
    dot = screen.getByLabelText(/^Status:/);
    expect(dot).toHaveClass("bg-muted-foreground/40");
    expect(dot).not.toHaveClass("border-2");
  });
});

describe("ActorAvatar fallback (LRM-201 single glyph + stable tone)", () => {
  it("agent fallback uses a stable tone class — not flat gray two-letter disc", () => {
    const { container } = render(
      <ActorAvatar actorType="agent" actorId="agent-1" profileLink={false} />,
    );
    const node = container.querySelector('[data-slot="avatar"]') as HTMLElement | null;
    expect(node).not.toBeNull();
    // LRM-201: missing/failed avatar → single glyph + stable palette (not bg-muted).
    expect(node!.style.backgroundColor).toBe("");
    expect(node!.style.color).toBe("");
    expect(node!.className).toMatch(/bg-\[#/);
    expect(node!.className).not.toContain("bg-muted");
    expect(node!.getAttribute("data-fallback")).toBe("true");
    expect((node!.textContent || "").trim().length).toBe(1);
  });

  it("member fallback also uses a stable tone class (no inline color)", () => {
    const { container } = render(
      <ActorAvatar actorType="member" actorId="user-1" profileLink={false} />,
    );
    const node = container.querySelector('[data-slot="avatar"]') as HTMLElement | null;
    expect(node).not.toBeNull();
    expect(node!.style.backgroundColor).toBe("");
    expect(node!.className).toMatch(/bg-\[#/);
  });
});

describe("ActorAvatar agent panel click (#349 app-wide)", () => {
  beforeEach(() => {
    openFromStoreMock.mockClear();
    openFromContextMock.mockClear();
    openInNewTabMock.mockClear();
    contextAvailable = false;
  });

  it("plain-clicking an agent avatar opens the panel via the global store when no context is present", () => {
    render(<ActorAvatar actorType="agent" actorId="agent-1" />);
    fireEvent.click(screen.getByRole("button"));
    // LRM-877 Dock Stack: open(agentId, snapshot?, { returnToMemberId? })
    expect(openFromStoreMock).toHaveBeenCalledWith(
      "agent-1",
      undefined,
      undefined,
    );
    expect(openInNewTabMock).not.toHaveBeenCalled();
  });

  it("prefers the local context over the global store when a provider is in scope", () => {
    contextAvailable = true;
    render(<ActorAvatar actorType="agent" actorId="agent-1" />);
    fireEvent.click(screen.getByRole("button"));
    expect(openFromContextMock).toHaveBeenCalledWith(
      "agent-1",
      undefined,
      undefined,
    );
    expect(openFromStoreMock).not.toHaveBeenCalled();
  });

  it("⌘/ctrl-clicking an agent avatar routes to the full detail page instead of the panel", () => {
    render(<ActorAvatar actorType="agent" actorId="agent-1" />);
    fireEvent.click(screen.getByRole("button"), { metaKey: true });
    expect(openInNewTabMock).toHaveBeenCalledWith("/agents/agent-1");
    expect(openFromStoreMock).not.toHaveBeenCalled();
    expect(openFromContextMock).not.toHaveBeenCalled();
  });

  it("defers to a control ancestor (menu item / row button) instead of opening the panel", () => {
    render(
      <button type="button" data-testid="row-button">
        <ActorAvatar actorType="agent" actorId="agent-1" />
      </button>,
    );
    // The avatar's own panel-trigger is the inner control; the outer row
    // button is the control ancestor it must defer to.
    const triggers = screen.getAllByRole("button");
    const avatarTrigger = triggers.find(
      (el) => el.getAttribute("data-testid") !== "row-button",
    )!;
    fireEvent.click(avatarTrigger);
    expect(openFromStoreMock).not.toHaveBeenCalled();
    expect(openFromContextMock).not.toHaveBeenCalled();
  });

  // LRM-809: an interactive ancestor that explicitly opts in via
  // data-avatar-profile-entry (Activity feed row) keeps the avatar's profile
  // entry alive — avatar click opens the panel, row click stays on the row.
  it("opens the panel inside a control ancestor carrying data-avatar-profile-entry", () => {
    const rowClick = vi.fn();
    render(
      <button type="button" data-testid="row-button" data-avatar-profile-entry="true" onClick={rowClick}>
        <ActorAvatar actorType="agent" actorId="agent-1" />
      </button>,
    );
    const triggers = screen.getAllByRole("button");
    const avatarTrigger = triggers.find(
      (el) => el.getAttribute("data-testid") !== "row-button",
    )!;
    fireEvent.click(avatarTrigger);
    expect(openFromStoreMock).toHaveBeenCalledWith(
      "agent-1",
      undefined,
      undefined,
    );
    // The avatar consumed the event — the row's own action must not fire.
    expect(rowClick).not.toHaveBeenCalled();
  });

  // LRM-809: human avatars get the same opt-in — member panel, not agent.
  it("opens the member panel inside a control ancestor carrying data-avatar-profile-entry", () => {
    openMemberFromStoreMock.mockClear();
    const rowClick = vi.fn();
    render(
      <button type="button" data-testid="row-button" data-avatar-profile-entry="true" onClick={rowClick}>
        <ActorAvatar actorType="member" actorId="user-1" />
      </button>,
    );
    const triggers = screen.getAllByRole("button");
    const avatarTrigger = triggers.find(
      (el) => el.getAttribute("data-testid") !== "row-button",
    )!;
    fireEvent.click(avatarTrigger);
    expect(openMemberFromStoreMock).toHaveBeenCalledWith("user-1");
    expect(rowClick).not.toHaveBeenCalled();
  });
});

describe("ActorAvatar enableHoverCard (task #25 — one sitewide identity card)", () => {
  it("renders ActorProfileContent for agents (not AgentProfileCard)", () => {
    render(
      <ActorAvatar actorType="agent" actorId="agent-1" enableHoverCard />,
    );
    const content = screen.getByTestId("actor-profile-content");
    expect(content).toHaveAttribute("data-member-type", "agent");
    expect(content).toHaveAttribute("data-member-id", "agent-1");
    expect(screen.getByTestId("hover-card")).toBeInTheDocument();
  });

  it("renders ActorProfileContent for members (not MemberProfileCard)", () => {
    render(
      <ActorAvatar actorType="member" actorId="user-9" enableHoverCard />,
    );
    const content = screen.getByTestId("actor-profile-content");
    expect(content).toHaveAttribute("data-member-type", "user");
    expect(content).toHaveAttribute("data-member-id", "user-9");
  });
});
