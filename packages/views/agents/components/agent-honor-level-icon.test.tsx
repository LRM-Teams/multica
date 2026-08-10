import { fireEvent, render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  AgentHonorLevelIcon,
  MAX_AGENT_HONOR_LEVEL,
  agentHonorLevelIconFallbackURL,
  agentHonorLevelIconURL,
  normalizeAgentHonorLevel,
} from "./agent-honor-level-icon";

describe("AgentHonorLevelIcon", () => {
  it("publishes one icon for every supported agent honor level", () => {
    expect(MAX_AGENT_HONOR_LEVEL).toBe(30);
    expect(agentHonorLevelIconURL(1)).toBe(
      "https://cdn.leagent.me/honor-assets/v1/agents/agent-honor-level-01.webp",
    );
    expect(agentHonorLevelIconURL(30)).toBe(
      "https://cdn.leagent.me/honor-assets/v1/agents/agent-honor-level-30.webp",
    );
  });

  it("clamps stale or invalid server levels to the available asset range", () => {
    expect(normalizeAgentHonorLevel(0)).toBe(1);
    expect(normalizeAgentHonorLevel(12.9)).toBe(12);
    expect(normalizeAgentHonorLevel(31)).toBe(30);
    expect(normalizeAgentHonorLevel(Number.NaN)).toBe(1);
  });

  it("renders a sized decorative image by default", () => {
    const { container } = render(<AgentHonorLevelIcon level={12} />);

    const icon = container.querySelector("img");
    expect(icon).not.toBeNull();
    expect(icon).toHaveAttribute("width", "256");
    expect(icon).toHaveAttribute("height", "256");
    expect(icon).toHaveAttribute("data-agent-honor-level", "12");

    fireEvent.error(icon!);
    expect(icon).toHaveAttribute("src", agentHonorLevelIconFallbackURL(12));
  });
});
