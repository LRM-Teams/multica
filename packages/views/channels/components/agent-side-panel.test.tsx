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
const { permission, usageRows } = vi.hoisted(() => ({
  permission: { allowed: false },
  usageRows: [] as Array<{
    agent_id: string;
    model: string;
    input_tokens: number;
    output_tokens: number;
    cache_read_tokens: number;
    cache_write_tokens: number;
    task_count: number;
  }>,
}));

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
  useQuery: (options: { kind?: string }) => ({
    data: options.kind === "usage-by-agent" ? usageRows : [],
    isLoading: false,
  }),
}));
vi.mock("@multica/core/runtimes", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"] }),
  runtimeHealthState: () => "ok",
}));
vi.mock("@multica/core/dashboard/queries", () => ({
  dashboardUsageByAgentOptions: () => ({ kind: "usage-by-agent" }),
}));
vi.mock("@multica/core/runtimes/custom-pricing-store", () => {
  const state = { pricings: {} as Record<string, unknown> };
  const useCustomPricingStore = Object.assign(
    (selector?: (value: typeof state) => unknown) => (selector ? selector(state) : state),
    { getState: () => state },
  );
  return { useCustomPricingStore, getCustomPricing: () => undefined };
});

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
    usage_section: "Usage",
    usage_reported_window: "Last 30 days · reported usage only",
    usage_loading: "Loading reported usage…",
    usage_empty: "No reported usage yet",
    usage_estimated_cost: "Estimated cost",
    usage_cost_unavailable: "Unavailable",
    usage_tokens: "Tokens",
    runtime_section: "Runtime Config",
  },
  inspector: {
    section_properties: "Properties",
    prop_runtime: "Runtime",
    prop_model: "Model",
    prop_visibility: "Visibility",
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

function renderPanel(
  currentUserId = "user-owner",
  managedRole?: "group_manager",
  variant?: "page",
) {
  return render(
    <AgentSidePanel
      agent={makeAgent("user-owner", managedRole)}
      currentUserId={currentUserId}
      members={members}
      onClose={() => {}}
      variant={variant}
    />,
  );
}

describe("AgentSidePanel", () => {
  afterEach(() => {
    vi.clearAllMocks();
    configStore.setState({ agentProfileDevAccessEnabled: false });
    permission.allowed = false;
    usageRows.length = 0;
  });

  it("keeps non-owner access to profile only by default", () => {
    renderPanel("user-other");
    expect(screen.getByText("Atlas")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Activity" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Files" })).not.toBeInTheDocument();
  });

  it("shows a standalone reported-usage card instead of a fake session-token baseline", () => {
    renderPanel("user-owner");
    expect(screen.getByRole("region", { name: "Usage" })).toBeInTheDocument();
    expect(screen.getByText("Last 30 days · reported usage only")).toBeInTheDocument();
    expect(screen.getByText("No reported usage yet")).toBeInTheDocument();
    expect(screen.queryByText("0 tokens")).not.toBeInTheDocument();
  });

  it("shows an estimated cost and secondary token total for reported usage", () => {
    usageRows.push({
      agent_id: "agent-1",
      model: "claude-sonnet-4-6",
      input_tokens: 1_000_000,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
      task_count: 1,
    });

    renderPanel("user-owner");

    expect(screen.getByText("Estimated cost")).toBeInTheDocument();
    expect(screen.getByText("$3.00")).toHaveClass("text-base", "font-semibold");
    expect(screen.getByText("Tokens")).toBeInTheDocument();
    expect(screen.getByText("1M")).toHaveClass("text-muted-foreground");
  });

  it("does not invent a cost when a reported model has no pricing", () => {
    usageRows.push({
      agent_id: "agent-1",
      model: "unpriced-model",
      input_tokens: 1_000,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
      task_count: 1,
    });

    renderPanel("user-owner");

    expect(screen.getByText("Unavailable")).toBeInTheDocument();
    expect(screen.getByText("1K")).toBeInTheDocument();
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

  it("keeps visited page tabs mounted in equal-width 44px mobile targets", () => {
    const { container } = renderPanel("user-owner", undefined, "page");

    expect(screen.queryByRole("button", { name: "Close panel" })).not.toBeInTheDocument();
    expect(container.querySelector("aside")).toHaveClass("min-w-0");
    expect(container.querySelector(".overflow-y-auto")).toHaveClass("min-w-0");
    for (const tab of ["Profile", "Activity", "Files"]) {
      expect(screen.getByRole("button", { name: tab })).toHaveClass(
        "min-h-11",
        "flex-1",
        "justify-center",
      );
    }
    expect(screen.getByRole("button", { name: "Activity" }).parentElement).toHaveClass("w-full", "px-0");

    fireEvent.click(screen.getByRole("button", { name: "Activity" }));
    expect(screen.getByText("Activity content")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Profile" }));
    expect(screen.getByText("Activity content")).toBeInTheDocument();
  });

  it("restores a visited page tab's scroll position after switching tabs", () => {
    const { container } = renderPanel("user-owner", undefined, "page");
    const tabBody = container.querySelector(".overflow-y-auto") as HTMLDivElement;

    fireEvent.click(screen.getByRole("button", { name: "Activity" }));
    tabBody.scrollTop = 128;

    fireEvent.click(screen.getByRole("button", { name: "Profile" }));
    tabBody.scrollTop = 24;

    fireEvent.click(screen.getByRole("button", { name: "Activity" }));
    expect(tabBody.scrollTop).toBe(128);
  });

  it("keeps desktop panel tabs content-width and left aligned", () => {
    renderPanel();

    const activityTab = screen.getByRole("button", { name: "Activity" });
    expect(activityTab).toHaveClass("shrink-0", "px-3");
    expect(activityTab).not.toHaveClass("flex-1", "justify-center", "min-h-11");
    expect(activityTab.parentElement).not.toHaveClass("w-full", "px-0");
  });

  it("never renders a separate Config tab (merged into Profile, #565)", () => {
    renderPanel("user-owner", "group_manager");
    expect(screen.queryByRole("button", { name: "Config" })).not.toBeInTheDocument();
    // Runtime config now lives inside the Profile view, not a separate tab, under
    // a Profile-specific "Runtime Config" title (not the shared "Properties").
    expect(screen.getByText("Runtime Config")).toBeInTheDocument();
    expect(screen.queryByText("Properties")).not.toBeInTheDocument();
    expect(screen.getByTestId("runtime-picker")).toBeInTheDocument();
    expect(screen.getByTestId("visibility-picker")).toBeInTheDocument();
    // Concurrency was dropped from the Profile runtime section (#565 fix-forward).
    expect(screen.queryByTestId("concurrency-picker")).not.toBeInTheDocument();
  });

  it("renders exactly the 4 runtime pickers, no Concurrency (#565 fix-forward)", () => {
    renderPanel("user-owner", "group_manager");
    for (const id of ["runtime-picker", "model-picker", "thinking-picker", "visibility-picker"]) {
      expect(screen.getByTestId(id)).toBeInTheDocument();
    }
    expect(screen.queryByTestId("concurrency-picker")).not.toBeInTheDocument();
  });

  it("renders EDITABLE runtime pickers in Profile for a group manager (any member)", () => {
    // permission stays denied — the group_manager override is what grants edit.
    renderPanel("user-other", "group_manager");

    for (const id of ["runtime-picker", "model-picker", "thinking-picker", "visibility-picker"]) {
      expect(screen.getByTestId(id)).toHaveAttribute("data-can-edit", "true");
    }
  });

  it("renders READ-ONLY runtime pickers in Profile for a non-owner, non-group-manager", () => {
    permission.allowed = false;
    renderPanel("user-other");

    for (const id of ["runtime-picker", "model-picker", "thinking-picker", "visibility-picker"]) {
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
