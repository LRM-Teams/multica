import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import zhCommon from "../locales/zh-Hans/common.json";
import { ActorStyledName } from "./actor-styled-name";

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (
      selector: (bundle: typeof zhCommon) => unknown,
      options?: Record<string, string | number>,
    ) => {
      const template = selector(zhCommon);
      if (typeof template !== "string") return String(template ?? "");
      return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) =>
        String(options?.[key] ?? `{{${key}}}`),
      );
    },
  }),
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
    expect(screen.getByAltText("第 42 级")).toBeInTheDocument();
    expect(screen.queryByTitle("Prism Core")).toBeNull();
  });

  it("keeps the agent level crest on agent identity surfaces", () => {
    const { container } = render(
      <ActorStyledName displayName="Aegis" agentHonorLevel={8} />,
    );

    expect(container.querySelector('[data-agent-honor-level="8"]')).not.toBeNull();
    expect(screen.getByAltText("第 8 级")).toBeInTheDocument();
    expect(container.querySelector("[data-user-honor-level]")).toBeNull();
  });

  it("does not substitute another badge when the Agent honor level is unavailable", () => {
    const { container } = render(<ActorStyledName displayName="Aegis" />);

    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByText("Aegis")).toBeInTheDocument();
  });
});
