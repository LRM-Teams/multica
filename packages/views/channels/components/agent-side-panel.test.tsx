// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { configStore } from "@multica/core/config";
import { AgentSidePanel } from "./agent-side-panel";

const filesPanelProps = vi.fn();

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: () => null,
}));

vi.mock("../../agents/components/agent-presence-status-line", () => ({
  AgentPresenceStatusLine: () => <span data-testid="presence-status" />,
}));

vi.mock("../../agents/components/tabs/activity-tab", () => ({
  ActivityTab: () => <div>Activity content</div>,
}));

vi.mock("./agent-files-panel", () => ({
  AgentFilesPanel: (props: {
    canReadFiles?: boolean;
    canEditFiles?: boolean;
  }) => {
    filesPanelProps(props);
    return <div>Files content</div>;
  },
}));

// The config tab reuses the agent-detail inspector pickers; stub them so the
// panel test stays focused on tab visibility/gating, not picker internals.
vi.mock("../../agents/components/inspector/runtime-picker", () => ({
  RuntimePicker: () => <div data-testid="runtime-picker" />,
}));
vi.mock("../../agents/components/inspector/model-picker", () => ({
  ModelPicker: () => <div data-testid="model-picker" />,
}));
vi.mock("../../agents/components/inspector/thinking-prop-row", () => ({
  ThinkingPropRow: () => <div data-testid="thinking-picker" />,
}));
vi.mock("../../agents/components/inspector/concurrency-picker", () => ({
  ConcurrencyPicker: () => <div data-testid="concurrency-picker" />,
}));
vi.mock("../../agents/components/inspector/visibility-picker", () => ({
  VisibilityPicker: () => <div data-testid="visibility-picker" />,
}));
vi.mock("../../common/prop-row", () => ({
  PropRow: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: [] }),
  useQueryClient: () => ({
    getQueryData: () => undefined,
    setQueryData: () => undefined,
    invalidateQueries: () => undefined,
  }),
}));
vi.mock("@multica/core/api", () => ({ api: { updateAgent: vi.fn() } }));
vi.mock("@multica/core/runtimes", () => ({ runtimeListOptions: () => ({ queryKey: ["runtimes"] }) }));
vi.mock("@multica/core/workspace/queries", () => ({
  workspaceKeys: { agents: (id: string) => ["agents", id] },
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: typeof RESOURCES) => string) => selector(RESOURCES),
  }),
}));

const RESOURCES = {
  tabs: {
    profile: "Profile",
    activity: "Activity",
    files: "Files",
    config: "Config",
  },
  side_panel: {
    close_aria: "Close panel",
    no_description: "No description",
    model_label: "Model",
    reasoning_label: "Reasoning",
    reasoning_default: "Default",
    runtime_label: "Runtime",
    runtime_cloud: "Cloud",
    created_label: "Created",
    owner_label: "Owner",
    config_shared_hint: "Any member can edit this group manager.",
  },
  inspector: {
    section_properties: "Properties",
    prop_runtime: "Runtime",
    prop_model: "Model",
    prop_visibility: "Visibility",
    prop_concurrency: "Concurrency",
  },
  detail: {
    update_failed_toast: "Update failed",
  },
};

const members: MemberWithUser[] = [
  {
    id: "m-owner",
    user_id: "user-owner",
    workspace_id: "ws-1",
    role: "member",
    name: "Owner",
    display_name: "Owner",
    email: "owner@example.com",
    avatar_url: null,
    profile_description: "",
    created_at: "2026-01-01T00:00:00Z",
  },
];

function makeAgent(ownerId = "user-owner", managedRole?: "group_manager"): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "runtime-1",
    name: "atlas",
    display_name: "Atlas",
    description: "Coordinates project context",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    visibility: "workspace",
    status: "idle",
    max_concurrent_tasks: 1,
    model: "",
    managed_role: managedRole,
    owner_id: ownerId,
    skills: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
  };
}

function renderPanel(currentUserId = "user-owner", managedRole?: "group_manager") {
  return render(
    <AgentSidePanel
      agent={makeAgent("user-owner", managedRole)}
      currentUserId={currentUserId}
      members={members}
      onClose={() => {}}
    />,
  );
}

describe("AgentSidePanel", () => {
  afterEach(() => {
    vi.clearAllMocks();
    configStore.setState({ agentProfileDevAccessEnabled: false });
  });

  it("keeps non-owner access to profile only by default", () => {
    renderPanel("user-other");
    expect(screen.getByText("Atlas")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Activity" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Files" })).not.toBeInTheDocument();
  });

  it("shows Activity and read-only Files tabs for non-owners in dev access mode", () => {
    configStore.setState({ agentProfileDevAccessEnabled: true });
    renderPanel("user-other");

    expect(screen.getByRole("button", { name: "Activity" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Files" }));

    expect(screen.getByText("Files content")).toBeInTheDocument();
    expect(filesPanelProps).toHaveBeenCalledWith(expect.objectContaining({
      canReadFiles: true,
      canEditFiles: false,
    }));
  });

  it("does not show a Config tab for ordinary agents", () => {
    renderPanel("user-owner");
    expect(screen.queryByRole("button", { name: "Config" })).not.toBeInTheDocument();
  });

  it("shows an editable Config tab for a group manager to any member (non-owner)", () => {
    renderPanel("user-other", "group_manager");

    const configTab = screen.getByRole("button", { name: "Config" });
    expect(configTab).toBeInTheDocument();
    fireEvent.click(configTab);

    // The shared-edit hint + reused inspector pickers render for any member.
    expect(screen.getByText("Any member can edit this group manager.")).toBeInTheDocument();
    expect(screen.getByTestId("runtime-picker")).toBeInTheDocument();
    expect(screen.getByTestId("concurrency-picker")).toBeInTheDocument();
  });
});
