import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ availability: "online", summary: null as null | { label: string; activityKind: string; detailKind: string } }));
vi.mock("@multica/core/agents", () => ({
  useAgentPresence: () => state.availability,
  useRunnerActivitySummary: () => ({ data: state.summary }),
}));
vi.mock("@multica/core/paths", () => ({ useCurrentWorkspace: () => ({ id: "workspace-1" }) }));
vi.mock("../../i18n", () => ({ useT: () => ({ t: (selector: (r: { availability: Record<string, string> }) => string) => selector({ availability: { online: "Online", offline: "Offline" } }) }) }));
vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("../../runtimes/components/provider-logo", () => ({ ProviderLogo: () => null }));

import { AgentActivityStatus } from "./agent-activity-list-item";

describe("AgentActivityStatus", () => {
  beforeEach(() => {
    state.availability = "online";
    state.summary = { label: "Running command...", activityKind: "working", detailKind: "running_command" };
  });

  it("derives command color and pulse from lifecycle facts", () => {
    const { container } = render(<AgentActivityStatus agentId="agent-1" />);
    expect(screen.getByTestId("agent-activity-status")).toHaveAttribute("data-activity-kind", "working");
    expect(container.querySelector(".bg-dot-working")).not.toBeNull();
    expect(container.querySelector(".animate-ping")).not.toBeNull();
  });

  it("derives thinking and error presentation from facts", () => {
    state.summary = { label: "Custom thinking copy", activityKind: "thinking", detailKind: "thinking_started" };
    const { container, rerender } = render(<AgentActivityStatus agentId="agent-1" />);
    expect(container.querySelector(".bg-blue-500")).not.toBeNull();
    state.availability = "offline";
    state.summary = { label: "Runtime unavailable", activityKind: "error", detailKind: "runtime_error" };
    rerender(<AgentActivityStatus agentId="agent-1" />);
    expect(container.querySelector(".bg-dot-fail")).not.toBeNull();
    expect(container.querySelector(".animate-ping")).toBeNull();
  });

  it("shows presence instead of stale non-error activity when offline", () => {
    state.availability = "offline";
    render(<AgentActivityStatus agentId="agent-1" />);
    expect(screen.getByTestId("agent-activity-status")).toHaveTextContent("Offline");
    expect(screen.queryByText("Running command...")).toBeNull();
  });
});
