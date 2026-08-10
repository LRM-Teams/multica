import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
  availability: "online" as "online" | "offline",
  summary: {
    label: "Running command...",
    tone: "warning",
    visibility: "visible",
  } as { label: string; tone: string; visibility: string } | null,
}));

vi.mock("@multica/core/agents", () => ({
  useAgentPresenceDetail: () => ({
    availability: state.availability,
    workload: "idle",
    runningCount: 0,
    queuedCount: 0,
    capacity: 1,
  }),
  useRunnerActivitySummary: () => ({ data: state.summary }),
  useRunnerActivity: () => {
    throw new Error("list Activity must not mount the per-Agent Timeline query");
  },
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "workspace-1" }),
}));

vi.mock("../use-agent-live-status", () => ({
  useAgentLiveStatus: () =>
    state.availability === "online"
      ? { label: "Online", dotClass: "bg-success", textClass: "text-success" }
      : { label: "Offline", dotClass: "bg-muted", textClass: "text-muted" },
}));

vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("../../runtimes/components/provider-logo", () => ({ ProviderLogo: () => null }));

import { AgentActivityStatus } from "./agent-activity-list-item";

describe("AgentActivityStatus", () => {
  beforeEach(() => {
    state.availability = "online";
    state.summary = {
      label: "Running command...",
      tone: "warning",
      visibility: "visible",
    };
  });

  it("shows a yellow pulse for dynamic work while the Agent is online", () => {
    const { container } = render(<AgentActivityStatus agentId="agent-1" />);
    expect(screen.getByTestId("agent-activity-status")).toHaveTextContent(
      "Running command...",
    );
    expect(container.querySelector(".animate-ping")).not.toBeNull();
  });

  it("shows Offline instead of stale work as soon as presence disconnects", () => {
    state.availability = "offline";
    render(<AgentActivityStatus agentId="agent-1" />);
    expect(screen.getByTestId("agent-activity-status")).toHaveTextContent("Offline");
    expect(screen.queryByText("Running command...")).toBeNull();
  });

  it("uses binary presence for non-dynamic Online summaries", () => {
    state.summary = { label: "Online", tone: "success", visibility: "visible" };
    render(<AgentActivityStatus agentId="agent-1" />);
    expect(screen.getByTestId("agent-activity-status")).toHaveTextContent("Online");
  });

  it("also treats thinking as yellow active work", () => {
    state.summary = { label: "Thinking...", tone: "info", visibility: "visible" };
    const { container } = render(<AgentActivityStatus agentId="agent-1" />);
    expect(screen.getByTestId("agent-activity-status")).toHaveTextContent("Thinking...");
    expect(container.querySelector(".animate-ping")).not.toBeNull();
    expect(screen.getByTestId("agent-activity-status").querySelector(".bg-warning")).not.toBeNull();
  });
});
