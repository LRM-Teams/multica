import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { GlobalAgentPanel } from "./global-agent-panel";

// The global panel is the no-slot fallback host for the #349 agent side panel
// (Agents/Runtimes/Projects/etc. that have no docked detail slot). It opens off
// the zustand `useAgentPanelStore` and renders the SAME AgentSidePanel as the
// docked channels/DM path, so it reads as one panel (Iris #447 parity).

let selectedAgentId: string | null = null;
let identitySnapshot: { display_name?: string } | null = null;
const closeMock = vi.fn(() => {
  selectedAgentId = null;
  identitySnapshot = null;
});

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string } | null }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"] }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: [] }),
}));

vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (
    selector: (s: {
      selectedAgentId: string | null;
      identitySnapshot: { display_name?: string } | null;
      close: () => void;
    }) => unknown,
  ) =>
    selector({
      selectedAgentId,
      identitySnapshot,
      close: closeMock,
    }),
  useAgentXpBurstStore: (selector: (s: { bursts: Record<string, never> }) => unknown) =>
    selector({ bursts: {} }),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ pathname: "/agents" }),
}));

vi.mock("../common/resolved-agent-side-panel", () => ({
  ResolvedAgentSidePanel: ({
    agentId,
    identitySnapshot: snap,
  }: {
    agentId: string;
    identitySnapshot?: { display_name?: string } | null;
  }) => (
    <div
      data-testid="agent-side-panel"
      data-agent-id={agentId}
      data-snapshot={snap?.display_name ?? ""}
    >
      {snap?.display_name ?? agentId}
    </div>
  ),
}));

describe("GlobalAgentPanel", () => {
  beforeEach(() => {
    selectedAgentId = null;
    identitySnapshot = null;
    closeMock.mockClear();
  });

  it("renders nothing when no agent is selected", () => {
    render(<GlobalAgentPanel />);
    expect(screen.queryByTestId("agent-side-panel")).toBeNull();
  });

  it("renders the selected agent's side panel when the store has a selection", () => {
    selectedAgentId = "agent-2";
    identitySnapshot = { display_name: "Atlas" };
    render(<GlobalAgentPanel />);

    const panel = screen.getByTestId("agent-side-panel");
    expect(panel).toHaveAttribute("data-agent-id", "agent-2");
    expect(panel).toHaveTextContent("Atlas");
  });

  it("opens even when the agent is absent from ListAgents (channel-only)", () => {
    selectedAgentId = "group-manager-1";
    render(<GlobalAgentPanel />);

    const panel = screen.getByTestId("agent-side-panel");
    expect(panel).toHaveAttribute("data-agent-id", "group-manager-1");
  });

  it("keeps the panel at the docked panel width for one-panel parity", () => {
    selectedAgentId = "agent-1";
    render(<GlobalAgentPanel />);

    // The Base UI Popup is portaled to the body; the panel is its direct child,
    // so the Popup carries the docked-parity width (default 360px / remembered).
    const popup = screen.getByTestId("agent-side-panel").parentElement;
    expect(popup?.style.width).toBe("360px");
  });
});
