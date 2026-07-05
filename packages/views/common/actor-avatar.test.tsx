import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentPresenceOverlay, AgentStatusDot } from "./actor-avatar";

// AgentStatusDot reads presence via useAgentPresenceDetail and the current
// workspace via useCurrentWorkspace. Default to "online + idle" so the dot
// renders; individual tests override the availability/workload per case.
type PresenceDetail = {
  availability: "online" | "unstable" | "offline" | "archived";
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

vi.mock("@multica/core/agents", () => ({
  useAgentPresenceDetail: () => presenceDetailMock(),
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1" }),
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/agents/${id}`,
    memberDetail: (id: string) => `/members/${id}`,
    squadDetail: (id: string) => `/squads/${id}`,
  }),
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
    expect(dot).toHaveClass("bg-muted-foreground/40");
  });

  it("renders nothing while presence is still loading", () => {
    presenceDetailMock.mockReturnValue("loading" as never);
    const { container } = render(<AgentStatusDot agentId="agent-1" size={28} />);
    expect(container).toBeEmptyDOMElement();
  });
});
