import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
  availability: "online" as "online" | "offline",
  summary: {
    label: "Command activity",
    tone: "running",
    visibility: "visible",
  } as { label: string; tone: string; visibility: string } | null,
}));

vi.mock("@multica/core/agents", () => ({
  useAgentPresence: () => state.availability,
  useRunnerActivitySummary: () => ({ data: state.summary }),
  useRunnerActivity: () => {
    throw new Error("list Activity must not mount the per-Agent Timeline query");
  },
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "workspace-1" }),
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (selector: (resources: { availability: Record<string, string> }) => string) =>
      selector({ availability: { online: "Online", offline: "Offline" } }),
  }),
}));

vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("../../runtimes/components/provider-logo", () => ({ ProviderLogo: () => null }));

import { AgentActivityStatus } from "./agent-activity-list-item";

describe("AgentActivityStatus", () => {
  beforeEach(() => {
    state.availability = "online";
    state.summary = {
      label: "Command activity",
      tone: "running",
      visibility: "visible",
    };
  });

  it("uses the server-projected running tone regardless of label", () => {
    const { container } = render(<AgentActivityStatus agentId="agent-1" />);
    expect(screen.getByTestId("agent-activity-status")).toHaveTextContent(
      "Command activity",
    );
    expect(container.querySelector(".animate-ping")).not.toBeNull();
    expect(container.querySelector(".bg-running")).not.toBeNull();
  });

  it("shows Offline instead of stale work as soon as presence disconnects", () => {
    state.availability = "offline";
    render(<AgentActivityStatus agentId="agent-1" />);
    expect(screen.getByTestId("agent-activity-status")).toHaveTextContent("Offline");
    expect(screen.queryByText("Command activity")).toBeNull();
  });

  it("shows a terminal error instead of Offline when presence disconnects", () => {
    state.availability = "offline";
    state.summary = {
      label: "Error: runtime failed: upstream unavailable",
      tone: "error",
      visibility: "visible",
    };
    const { container } = render(<AgentActivityStatus agentId="agent-1" />);
    const status = screen.getByTestId("agent-activity-status");
    expect(status).toHaveTextContent("Error: runtime failed: upstream unavailable");
    expect(status).not.toHaveTextContent("Offline");
    expect(status).toHaveAttribute("data-activity-tone", "error");
    expect(container.querySelector(".bg-destructive")).not.toBeNull();
  });

  it("uses binary presence for non-dynamic Online summaries", () => {
    state.summary = { label: "Online", tone: "success", visibility: "visible" };
    render(<AgentActivityStatus agentId="agent-1" />);
    expect(screen.getByTestId("agent-activity-status")).toHaveTextContent("Online");
  });

  it("uses the shared blue info tone for thinking", () => {
    state.summary = { label: "Thinking...", tone: "info", visibility: "visible" };
    const { container } = render(<AgentActivityStatus agentId="agent-1" />);
    expect(screen.getByTestId("agent-activity-status")).toHaveTextContent("Thinking...");
    expect(container.querySelector(".animate-ping")).not.toBeNull();
    expect(screen.getByTestId("agent-activity-status").querySelector(".bg-blue-500")).not.toBeNull();
  });

  it("uses the shared amber warning tone for working", () => {
    state.summary = { label: "Working...", tone: "warning", visibility: "visible" };
    const { container } = render(<AgentActivityStatus agentId="agent-1" />);
    expect(screen.getByTestId("agent-activity-status")).toHaveTextContent("Working...");
    expect(container.querySelector(".bg-running")).toBeNull();
    expect(container.querySelector(".bg-amber-500")).not.toBeNull();
  });
});
