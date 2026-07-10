import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { GlobalAgentPanel } from "./global-agent-panel";

// The global panel is the no-slot fallback host for the #349 agent side panel
// (Agents/Runtimes/Projects/etc. that have no docked detail slot). It opens off
// the zustand `useAgentPanelStore` and renders the SAME AgentSidePanel as the
// docked channels/DM path, so it reads as one panel (Iris #447 parity).

const mockAgents = [
  { id: "agent-1", name: "Nova" },
  { id: "agent-2", name: "Atlas" },
];

let selectedAgentId: string | null = null;
const closeMock = vi.fn(() => {
  selectedAgentId = null;
});

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string } | null }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["agents"] }),
  memberListOptions: () => ({ queryKey: ["members"] }),
}));

vi.mock("@tanstack/react-query", () => ({
  // Both the agent and member queries resolve to a stable list. The member
  // list only feeds the (mocked) AgentSidePanel, so returning the agent list
  // for both is harmless and keeps the mock trivial.
  useQuery: () => ({ data: mockAgents }),
}));

vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (selector: (s: { selectedAgentId: string | null; close: () => void }) => unknown) =>
    selector({ selectedAgentId, close: closeMock }),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ pathname: "/agents" }),
}));

vi.mock("../channels/components/agent-side-panel", () => ({
  AgentSidePanel: ({ agent }: { agent: { id: string; name: string } }) => (
    <div data-testid="agent-side-panel" data-agent-id={agent.id}>
      {agent.name}
    </div>
  ),
}));

describe("GlobalAgentPanel", () => {
  beforeEach(() => {
    selectedAgentId = null;
    closeMock.mockClear();
  });

  it("renders nothing when no agent is selected", () => {
    render(<GlobalAgentPanel />);
    expect(screen.queryByTestId("agent-side-panel")).toBeNull();
  });

  it("renders the selected agent's side panel when the store has a selection", () => {
    selectedAgentId = "agent-2";
    render(<GlobalAgentPanel />);

    const panel = screen.getByTestId("agent-side-panel");
    expect(panel).toHaveAttribute("data-agent-id", "agent-2");
    expect(panel).toHaveTextContent("Atlas");
  });

  it("keeps the panel at the docked panel width (440px) for one-panel parity", () => {
    selectedAgentId = "agent-1";
    render(<GlobalAgentPanel />);

    // The Base UI Popup is portaled to the body; the panel is its direct child,
    // so the Popup carries the docked-parity width class.
    const popup = screen.getByTestId("agent-side-panel").parentElement;
    expect(popup?.className).toContain("w-[440px]");
  });
});
