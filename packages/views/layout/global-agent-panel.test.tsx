import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { GlobalAgentPanel } from "./global-agent-panel";
import {
  PROFILE_PANEL_WIDTH_DEFAULT,
  PROFILE_PANEL_WIDTH_STORAGE_KEY,
} from "./use-profile-panel-width";

// The global panel is the no-slot fallback host for the #349 agent side panel
// (Agents/Runtimes/Projects/etc. that have no docked detail slot). It opens off
// the zustand `useAgentPanelStore` and renders the SAME AgentSidePanel as the
// docked channels/DM path, so it reads as one panel (Iris #447 parity).

let selectedAgentId: string | null = null;
let identitySnapshot: { display_name?: string } | null = null;
let returnToMemberId: string | null = null;
const closeMock = vi.fn(() => {
  selectedAgentId = null;
  identitySnapshot = null;
  returnToMemberId = null;
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
      returnToMemberId: string | null;
      close: () => void;
    }) => unknown,
  ) =>
    selector({
      selectedAgentId,
      identitySnapshot,
      returnToMemberId,
      close: closeMock,
    }),
  useAgentXpBurstStore: (selector: (s: { bursts: Record<string, never> }) => unknown) =>
    selector({ bursts: {} }),
}));

vi.mock("@multica/core/workspace", () => ({
  useMemberPanelStore: (
    selector: (s: { open: (id: string) => void }) => unknown,
  ) => selector({ open: vi.fn() }),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ pathname: "/agents" }),
}));

vi.mock("../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (keys: { side_panel: { resize_aria: string } }) => string) =>
      fn({ side_panel: { resize_aria: "Resize profile panel" } }),
  }),
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
    returnToMemberId = null;
    closeMock.mockClear();
    window.localStorage.removeItem(PROFILE_PANEL_WIDTH_STORAGE_KEY);
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

  it("defaults to the docked panel width (520px) with a left-edge resize handle (LRM-481)", () => {
    selectedAgentId = "agent-1";
    render(<GlobalAgentPanel />);

    const popup = screen.getByTestId("global-agent-panel");
    expect(popup).toHaveStyle({ width: `${PROFILE_PANEL_WIDTH_DEFAULT}px` });
    expect(screen.getByTestId("global-agent-panel-resize")).toHaveAttribute(
      "aria-label",
      "Resize profile panel",
    );
  });

  it("restores a persisted width from localStorage (LRM-481)", async () => {
    window.localStorage.setItem(PROFILE_PANEL_WIDTH_STORAGE_KEY, "520");
    selectedAgentId = "agent-1";
    render(<GlobalAgentPanel />);

    await vi.waitFor(() => {
      expect(screen.getByTestId("global-agent-panel")).toHaveStyle({ width: "520px" });
    });
  });
});
