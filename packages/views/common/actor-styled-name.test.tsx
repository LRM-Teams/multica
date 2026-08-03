import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ActorStyledName } from "./actor-styled-name";

vi.mock("../agents/hooks/use-agent-fleet-class-name", () => ({
  useAgentFleetClassName: () => (_classId: string, label: string) => label,
}));

describe("ActorStyledName", () => {
  it("uses the user's level crest instead of an equipped achievement on identity surfaces", () => {
    const { container } = render(
      <ActorStyledName
        displayName="Frank"
        honor={{
          level: 42,
          name_style: "default",
          equipped_badge: {
            id: "prism_core",
            title: "Prism Core",
            description: "Achievement",
            svg_key: "prism_core",
          },
        }}
      />,
    );

    expect(screen.getByText("Frank")).toBeInTheDocument();
    expect(container.querySelector('[data-user-honor-level="42"]')).not.toBeNull();
    expect(screen.queryByTitle("Prism Core")).toBeNull();
  });

  it("keeps the agent level crest on agent identity surfaces", () => {
    const { container } = render(
      <ActorStyledName displayName="Aegis" agentHonorLevel={8} />,
    );

    expect(container.querySelector('[data-agent-honor-level="8"]')).not.toBeNull();
    expect(container.querySelector("[data-user-honor-level]")).toBeNull();
  });
});
