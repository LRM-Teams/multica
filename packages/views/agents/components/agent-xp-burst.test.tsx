import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AgentXpBurst } from "./agent-xp-burst";
import { useAgentXpBurstStore } from "@multica/core/agents/stores";

describe("AgentXpBurst", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    useAgentXpBurstStore.setState({ bursts: {} });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows ring and chip when the agent receives a burst", () => {
    render(
      <AgentXpBurst agentId="agent-1">
        <span data-testid="face">avatar</span>
      </AgentXpBurst>,
    );

    act(() => {
      useAgentXpBurstStore.setState({
        bursts: { "agent-1": { burstKey: 1, delta: 2, fileKey: "memory" } },
      });
    });

    expect(screen.getByTestId("agent-xp-burst-ring")).toBeInTheDocument();
    expect(screen.getByTestId("agent-xp-burst-chip")).toHaveTextContent("+2");
  });
});
