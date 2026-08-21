import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ExecutionRow } from "../execution-overlay";
import { renderWithI18n } from "../../test/i18n";
import { ResearchAgentInspector } from "./research-agent-inspector";

// The row/inspector now render the site-wide smart avatar, which resolves
// identity through workspace queries. These suites are about execution copy.
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid={`actor-avatar-${actorId}`} />
  ),
}));

const row: ExecutionRow = {
  id: "agent-archived",
  name: "agent-archived",
  role: "Agent",
  status: "unknown",
  actionKey: "unknown",
};

describe("ResearchAgentInspector status localization", () => {
  it("localizes the fallback execution status", () => {
    renderWithI18n(
      <ResearchAgentInspector
        row={row}
        open
        onClose={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );

    const inspector = screen.getByTestId("research-agent-inspector");
    expect(inspector).toHaveTextContent("未知");
    expect(inspector).not.toHaveTextContent("unknown");
  });

  it("preserves canonical live activity instead of replacing it with a status", () => {
    renderWithI18n(
      <ResearchAgentInspector
        row={{ ...row, status: "running", action: "正在核验监管原文" }}
        open
        onClose={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );

    expect(screen.getByTestId("research-agent-inspector")).toHaveTextContent(
      "正在核验监管原文",
    );
  });
});
