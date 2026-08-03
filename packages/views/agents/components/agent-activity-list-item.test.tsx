// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { AgentPresenceDetail } from "@multica/core/agents";
import { AgentActivityListItem } from "./agent-activity-list-item";

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid="avatar">{actorId}</span>
  ),
}));

vi.mock("../../runtimes/components/provider-logo", () => ({
  ProviderLogo: ({ provider }: { provider: string }) => (
    <span data-testid="provider-logo">{provider}</span>
  ),
}));

function presence(
  over: Partial<AgentPresenceDetail> = {},
): AgentPresenceDetail {
  return {
    agent_id: "a1",
    availability: "online",
    workload: "idle",
    ...over,
  } as AgentPresenceDetail;
}

describe("AgentActivityListItem", () => {
  it("renders name, runtime, Idle activity", () => {
    render(
      <AgentActivityListItem
        agentId="a1"
        displayName="Atlas"
        provider="cursor"
        runtimeLabel="Cursor"
        presence={presence({ workload: "idle" })}
      />,
    );
    expect(screen.getByTestId("agent-activity-list-item")).toBeInTheDocument();
    expect(screen.getByText("Atlas")).toBeInTheDocument();
    expect(screen.getByTestId("agent-activity-list-item-runtime")).toHaveTextContent(
      "Cursor",
    );
    expect(screen.getByTestId("agent-activity-list-item-activity")).toHaveTextContent(
      "Idle",
    );
  });

  it("shows Working with spinner for working workload", () => {
    render(
      <AgentActivityListItem
        agentId="a1"
        displayName="Wendy"
        provider="cursor"
        runtimeLabel="Cursor"
        presence={presence({ workload: "working" })}
      />,
    );
    expect(screen.getByTestId("agent-activity-list-item-activity")).toHaveTextContent(
      "Working",
    );
  });

  it("fires onClick", () => {
    const onClick = vi.fn();
    render(
      <AgentActivityListItem
        agentId="a1"
        displayName="Atlas"
        presence={presence()}
        onClick={onClick}
      />,
    );
    fireEvent.click(screen.getByTestId("agent-activity-list-item"));
    expect(onClick).toHaveBeenCalled();
  });
});
