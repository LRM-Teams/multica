import type { ReactElement } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AgentPresenceStatusLine } from "./agent-presence-status-line";

const formatMock = vi.fn<() => string | null>();
const visualMock = vi.fn<() => { icon: () => ReactElement; textClass: string } | null>();

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/agents", () => ({
  useAgentPresenceDetail: () => ({ availability: "online", workload: "idle" }),
}));
vi.mock("../../i18n", () => ({ useT: () => ({ t: (x: unknown) => x }) }));
vi.mock("@multica/ui/components/ui/skeleton", () => ({
  Skeleton: () => <div data-testid="presence-skeleton" />,
}));
vi.mock("../presence", () => ({
  formatPresenceStatus: () => formatMock(),
  presenceStatusVisual: () => visualMock(),
}));

describe("AgentPresenceStatusLine", () => {
  it("renders the localized status word + icon when presence resolves", () => {
    formatMock.mockReturnValue("Online");
    visualMock.mockReturnValue({
      icon: () => <svg data-testid="status-icon" />,
      textClass: "text-success",
    });

    render(<AgentPresenceStatusLine agentId="agent-1" />);

    expect(screen.getByText("Online")).toBeInTheDocument();
    expect(screen.getByTestId("status-icon")).toBeInTheDocument();
    expect(screen.queryByTestId("presence-skeleton")).toBeNull();
  });

  it("renders a skeleton (not an empty gap) while presence is unknown", () => {
    formatMock.mockReturnValue(null);
    visualMock.mockReturnValue(null);

    render(<AgentPresenceStatusLine agentId="agent-1" />);

    expect(screen.getByTestId("presence-skeleton")).toBeInTheDocument();
  });
});
