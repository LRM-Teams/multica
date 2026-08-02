import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ResearchFleetAvatarStack } from "./research-fleet-avatar-stack";

const openAgentPanel = vi.fn();

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (picker: (keys: Record<string, unknown>) => unknown) => {
      const keys = {
        panel: {
          fleet: "Fleet",
          fleet_empty_title: "No fleet",
          fleet_empty_body: "Empty body",
          fleet_loading_body: "Loading fleet",
          fleet_mode: {
            empty: "Idle",
            loading: "Assembling",
            running: "Running",
            done: "Done",
          },
        },
        overlay: { fleet_collapse: "Collapse", fleet_expand: "Expand" },
      };
      return picker(keys as never);
    },
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({
    actorId,
    profileLink,
  }: {
    actorId: string;
    profileLink?: boolean;
  }) =>
    profileLink ? (
      <button
        type="button"
        data-testid={`avatar-${actorId}`}
        onClick={(e) => {
          e.stopPropagation();
          openAgentPanel(actorId);
        }}
      >
        avatar
      </button>
    ) : (
      <span data-testid={`avatar-${actorId}`}>avatar</span>
    ),
}));

describe("ResearchFleetAvatarStack (LRM-776)", () => {
  beforeEach(() => {
    openAgentPanel.mockClear();
  });

  it("keeps avatar profile entry on the expand control so clicks open the agent panel", () => {
    render(
      <ResearchFleetAvatarStack
        members={[
          {
            id: "m1",
            agent_id: "agent-1",
            name: "Scout",
            display_name: "Scout",
            role: "scout",
            status: "active",
            is_lead: false,
          },
        ]}
      />,
    );

    const toggle = screen.getByTestId("research-fleet-avatar-stack-toggle");
    expect(toggle).toHaveAttribute("data-avatar-profile-entry", "true");

    fireEvent.click(screen.getByTestId("avatar-agent-1"));
    expect(openAgentPanel).toHaveBeenCalledWith("agent-1");
  });
});
