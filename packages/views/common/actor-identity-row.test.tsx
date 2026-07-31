import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ActorIdentityRow } from "./actor-identity-row";

describe("ActorIdentityRow", () => {
  it("renders display name with weak @handle when they differ", () => {
    render(
      <ActorIdentityRow identity={{ display_name: "Alice Zhang", name: "alice" }} />,
    );

    expect(screen.getByText("Alice Zhang")).toBeInTheDocument();
    expect(screen.getByText("@alice")).toBeInTheDocument();
  });

  it("hides @handle when display name falls back to handle", () => {
    render(<ActorIdentityRow identity={{ display_name: "", name: "alice" }} />);

    expect(screen.getByText("alice")).toBeInTheDocument();
    expect(screen.queryByText("@alice")).not.toBeInTheDocument();
  });

  it("supports explicit displayName and handle overrides", () => {
    render(<ActorIdentityRow displayName="Aegis" handle="agent_aegis" />);

    expect(screen.getByText("Aegis")).toBeInTheDocument();
    expect(screen.getByText("@agent_aegis")).toBeInTheDocument();
  });

  it("keeps honor name color while hiding badges in dense lists", () => {
    render(
      <ActorIdentityRow
        displayName="Aurora"
        honor={{
          level: 24,
          name_style: "gold",
          equipped_badge: {
            id: "first-delivery",
            title: "First delivery",
            description: "Delivered the first issue",
            svg_key: "delivery",
          },
        }}
        fleet={{
          agent_id: "aurora",
          fleet_score: 720,
          class_id: "frigate",
          class_label: "Frigate",
          fleet_rank: 2,
          fleet_size: 12,
          sample_tasks: 8,
          sample_sufficient: true,
          frozen: false,
          pillars: {
            delivery: 80,
            evolution: 72,
            growth: 68,
            efficiency: 74,
          },
        }}
        showBadges={false}
      />,
    );

    expect(screen.getByText("Aurora")).toHaveClass("honor-name--gold");
    expect(screen.queryByTitle("First delivery")).not.toBeInTheDocument();
    expect(screen.queryByTitle("Frigate")).not.toBeInTheDocument();
  });
});
