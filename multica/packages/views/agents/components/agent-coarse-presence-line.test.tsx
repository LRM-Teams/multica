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

describe("AgentCoarsePresenceLine (LRM-248)", () => {
  beforeEach(() => {
    cleanup();
    mockPresence.current = "loading";
  });

  it("shows Online as plain text — no second status dot", () => {
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
    // LRM-248: profile/DM header text has no second round indicator.
    expect(mark.querySelector(".rounded-full")).toBeNull();
  });

  it("shows Online while a task runs — never Working", () => {
    mockPresence.current = {
      availability: "online",
      workload: "working",
      runningCount: 1,
      capacity: 2,
      queuedCount: 0,
    };
    renderLine();
    const mark = screen.getByTestId("agent-live-status");
    expect(mark).toHaveTextContent("Online");
    expect(mark).not.toHaveTextContent(/Working|command|Reading/i);
  });

  it("folds unstable → Online", () => {
    mockPresence.current = {
      availability: "unstable",
      workload: "idle",
      runningCount: 0,
      capacity: 1,
      queuedCount: 0,
    };
    renderLine();
    expect(screen.getByTestId("agent-live-status")).toHaveTextContent("Online");
  });

  it("shows Offline when the runtime is down", () => {
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
