// @vitest-environment jsdom

import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  AgentHonorAchievementIcon,
  agentAchievementIconLevel,
} from "./agent-honor-achievement-icon";

describe("AgentHonorAchievementIcon", () => {
  it("maps achievement rarity across entry, advanced, and flagship warships", () => {
    expect(agentAchievementIconLevel(10)).toBe(1);
    expect(agentAchievementIconLevel(65)).toBe(19);
    expect(agentAchievementIconLevel(95)).toBe(30);
    expect(agentAchievementIconLevel(Number.NaN)).toBe(1);
  });

  it("keeps locked achievements on their warship while applying the locked treatment", () => {
    const { container } = render(
      <AgentHonorAchievementIcon rarity={78} title="Unbroken Orbit" locked />,
    );

    const icon = container.querySelector("[data-agent-achievement-icon='warship']");
    expect(icon).toHaveAttribute("data-agent-achievement-level", "23");
    expect(icon).toHaveAttribute("data-agent-achievement-locked", "true");
    expect(icon).toHaveClass("grayscale", "saturate-0");
    expect(icon?.querySelector("img")).toHaveAttribute("alt", "Unbroken Orbit");
  });
});
