import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AgentPresenceStatusLine } from "./agent-presence-status-line";

const liveStatusMock = vi.fn<() => { label: string; textClass: string; dotClass: string } | null>();

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("../use-agent-live-status", () => ({
  useAgentLiveStatus: () => liveStatusMock(),
}));

describe("AgentPresenceStatusLine", () => {
  it("renders the live status word without a second dot (LRM-248)", () => {
    liveStatusMock.mockReturnValue({
      label: "Offline",
      textClass: "text-muted-foreground",
      dotClass: "bg-muted-foreground/40",
    });

    render(<AgentPresenceStatusLine agentId="agent-1" />);

    const mark = screen.getByTestId("agent-live-status");
    expect(mark).toHaveTextContent("Offline");
    // Avatar owns the round indicator; name-row is text-only.
    expect(mark.querySelector(".rounded-full")).toBeNull();
    expect(screen.queryByTestId("presence-skeleton")).toBeNull();
  });

  it("renders a skeleton (not an empty gap) while status is unknown", () => {
    liveStatusMock.mockReturnValue(null);

    render(<AgentPresenceStatusLine agentId="agent-1" />);

    expect(screen.getByTestId("presence-skeleton")).toBeInTheDocument();
  });
});
