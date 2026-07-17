// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { configStore } from "@multica/core/config";
import { AgentSidePanel } from "./agent-side-panel";

const filesPanelProps = vi.fn();

// Per-test permission decision for the merged runtime-config section. Group
// managers override this to always-editable inside the component, so leaving
// it denied by default lets us assert the read-only path for ordinary agents.
const { permission } = vi.hoisted(() => ({ permission: { allowed: false } }));

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

// The runtime-config section reuses the agent-detail inspector pickers; stub
// them so the panel test stays focused on gating/visibility, not picker
// internals. Each stub echoes `canEdit` so we can assert the permission split.
vi.mock("../../agents/components/inspector/runtime-picker", () => ({
  RuntimePicker: (p: { canEdit?: boolean }) => (
    <div data-testid="runtime-picker" data-can-edit={String(!!p.canEdit)} />
  ),
}));
vi.mock("../../agents/components/inspector/model-picker", () => ({
  ModelPicker: (p: { canEdit?: boolean }) => (
    <div data-testid="model-picker" data-can-edit={String(!!p.canEdit)} />
  ),
}));
vi.mock("../../agents/components/inspector/thinking-prop-row", () => ({
  ThinkingPropRow: (p: { canEdit?: boolean }) => (
    <div data-testid="thinking-picker" data-can-edit={String(!!p.canEdit)} />
  ),
}));
vi.mock("../../agents/components/inspector/concurrency-picker", () => ({
  ConcurrencyPicker: (p: { canEdit?: boolean }) => (
    <div data-testid="concurrency-picker" data-can-edit={String(!!p.canEdit)} />
  ),
}));
vi.mock("../../agents/components/inspector/visibility-picker", () => ({
  VisibilityPicker: (p: { canEdit?: boolean }) => (
    <div data-testid="visibility-picker" data-can-edit={String(!!p.canEdit)} />
  ),
}));
vi.mock("../../common/prop-row", () => ({
  PropRow: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));
vi.mock("../../agents/hooks/use-update-agent", () => ({
  useUpdateAgent: () => vi.fn(),
}));
vi.mock("@multica/core/permissions", () => ({
  useAgentPermissions: () => ({ canEdit: permission }),
}));
vi.mock("../../runtimes/components/shared", () => ({
  useRuntimeHealthStateLabel: () => (state: string) => state,
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: [] }),
}));
vi.mock("@multica/core/runtimes", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"] }),
  runtimeHealthState: () => "ok",
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
    created_label: "Created",
    owner_label: "Owner",
  },
  inspector: {
    section_properties: "Properties",
    prop_runtime: "Runtime",
    prop_model: "Model",
    prop_visibility: "Visibility",
    prop_concurrency: "Concurrency",
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
    permission.allowed = false;
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

  it("never renders a separate Config tab (merged into Profile, #565)", () => {
    renderPanel("user-owner", "group_manager");
    expect(screen.queryByRole("button", { name: "Config" })).not.toBeInTheDocument();
    // Runtime config now lives inside the Profile view, not a separate tab.
    expect(screen.getByTestId("runtime-picker")).toBeInTheDocument();
    expect(screen.getByTestId("concurrency-picker")).toBeInTheDocument();
    expect(screen.getByTestId("visibility-picker")).toBeInTheDocument();
  });

  it("renders EDITABLE runtime pickers in Profile for a group manager (any member)", () => {
    // permission stays denied — the group_manager override is what grants edit.
    renderPanel("user-other", "group_manager");

    for (const id of ["runtime-picker", "model-picker", "thinking-picker", "visibility-picker", "concurrency-picker"]) {
      expect(screen.getByTestId(id)).toHaveAttribute("data-can-edit", "true");
    }
  });

  it("renders READ-ONLY runtime pickers in Profile for a non-owner, non-group-manager", () => {
    permission.allowed = false;
    renderPanel("user-other");

    for (const id of ["runtime-picker", "model-picker", "thinking-picker", "visibility-picker", "concurrency-picker"]) {
      expect(screen.getByTestId(id)).toHaveAttribute("data-can-edit", "false");
    }
  });

  it("threads the owner/admin permission decision into the runtime pickers", () => {
    // For an ordinary agent, editability comes straight from useAgentPermissions.
    permission.allowed = true;
    renderPanel("user-owner");

    expect(screen.getByTestId("runtime-picker")).toHaveAttribute("data-can-edit", "true");
  });
});
