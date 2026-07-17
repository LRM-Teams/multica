// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";

const TEST_RESOURCES = {
  en: { common: enCommon, agents: enAgents },
};

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

const mockPresence = vi.hoisted(() => ({ current: "loading" as unknown }));

vi.mock("@multica/core/agents", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/agents")>(
    "@multica/core/agents",
  );
  return {
    ...actual,
    useAgentPresenceDetail: () => mockPresence.current,
  };
});

import { AgentCoarsePresenceLine } from "./agent-coarse-presence-line";

function renderLine() {
  return render(
    <I18nProvider resources={TEST_RESOURCES} locale="en">
      <AgentCoarsePresenceLine agentId="agent-1" />
    </I18nProvider>,
  );
}

describe("AgentCoarsePresenceLine", () => {
  beforeEach(() => {
    cleanup();
    mockPresence.current = "loading";
  });

  it("shows the coarse presence word 'Online', not a fine action verb", () => {
    mockPresence.current = {
      availability: "online",
      workload: "idle",
      runningCount: 0,
      capacity: 1,
      queuedCount: 0,
    };
    renderLine();
    const mark = screen.getByTestId("agent-live-status");
    expect(mark).toHaveTextContent("Online");
    expect(mark.querySelector(".rounded-full")).not.toBeNull();
  });

  it("shows the coarse workload word 'Working' while a task runs (never the tool verb)", () => {
    mockPresence.current = {
      availability: "online",
      workload: "working",
      runningCount: 1,
      capacity: 2,
      queuedCount: 0,
    };
    renderLine();
    const mark = screen.getByTestId("agent-live-status");
    expect(mark).toHaveTextContent("Working");
    // Coarse only — no fine action verb echoed from the composer line.
    expect(mark).not.toHaveTextContent(/command|Reading|Writing/i);
  });

  it("shows 'Offline' when the runtime is down", () => {
    mockPresence.current = {
      availability: "offline",
      workload: "idle",
      runningCount: 0,
      capacity: 1,
      queuedCount: 0,
    };
    renderLine();
    expect(screen.getByTestId("agent-live-status")).toHaveTextContent("Offline");
  });

  it("renders a width-stable skeleton while presence is loading", () => {
    mockPresence.current = "loading";
    renderLine();
    expect(screen.getByTestId("presence-skeleton")).toBeInTheDocument();
  });
});
