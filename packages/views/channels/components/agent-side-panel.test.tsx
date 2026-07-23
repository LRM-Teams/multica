// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { configStore } from "@multica/core/config";
import { AgentSidePanel } from "./agent-side-panel";

const filesPanelProps = vi.fn();

const openDMMocks = vi.hoisted(() => ({
  openDM: vi.fn(),
  isPending: false,
}));

// Per-test permission decision for the merged runtime-config section. Group
// managers override this to always-editable inside the component, so leaving
// it denied by default lets us assert the read-only path for ordinary agents.
const { permission, usageRows, mockRuntimes } = vi.hoisted(() => ({
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
  // Runtimes returned by the mocked runtime-list query. Empty by default so the
  // existing tests see no selected runtime; a #687 test loads a staged one.
  mockRuntimes: { current: [] as Array<Record<string, unknown>> },
}));

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: () => null,
}));

vi.mock("../../common/actor-avatar", () => ({
  AgentPresenceOverlay: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="agent-presence-overlay">{children}</div>
  ),
}));

vi.mock("../../agents/components/tabs/activity-tab", () => ({
  ActivityTab: () => <div>Activity content</div>,
}));

vi.mock("../../agents/components/tabs/reminders-tab", () => ({
  RemindersTab: () => <div>Reminders content</div>,
}));

vi.mock("../../agents/components/agent-profile-actions", () => ({
  AgentProfileActions: ({ canManage }: { canManage: boolean }) => (
    <div data-testid="agent-profile-actions" data-can-manage={String(canManage)}>
      <button type="button" data-testid="agent-profile-action-message">
        Message
      </button>
    </div>
  ),
}));

vi.mock("../../agents/components/inline-field-editor", () => ({
  InlineFieldEditor: ({
    value,
    emptyLabel,
    testId = "inline-field",
  }: {
    value: string;
    emptyLabel?: string;
    testId?: string;
  }) => (
    <button type="button" data-testid={`${testId}-trigger`}>
      {value || emptyLabel || "edit"}
    </button>
  ),
}));

vi.mock("../../agents/components/agent-xp-burst", () => ({
  AgentXpBurst: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

vi.mock("../../agents/components/char-counter", () => ({
  CharCounter: () => null,
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
vi.mock("../../common/use-open-dm", () => ({
  useOpenDM: () => openDMMocks,
}));
vi.mock("@multica/core/permissions", () => ({
  useAgentPermissions: () => ({ canEdit: permission }),
}));
vi.mock("../../runtimes/components/shared", () => ({
  useRuntimeHealthStateLabel: () => (state: string) => state,
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { kind?: string; queryKey?: unknown[] }) => ({
    data:
      options.kind === "usage-by-agent"
        ? usageRows
        : options.queryKey?.[0] === "runtimes"
          ? mockRuntimes.current
          : [],
    isLoading: false,
  }),
}));
vi.mock("@multica/core/runtimes", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"] }),
  // Real staged-override behavior so the panel proves it consumes the shared
  // presentation (update_state) rather than raw runtime_health.
  deriveRuntimeHealthPresentation: (rt: { update_state?: string; runtime_health?: string }) =>
    rt?.update_state === "ready_to_apply"
      ? "ready_to_apply"
      : rt?.update_state === "pending" || rt?.update_state === "running"
        ? "updating"
        : (rt?.runtime_health ?? "ok"),
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
    reminders: "Reminders",
    files: "Files",
    usage: "Usage",
    config: "Config",
  },
  side_panel: {
    close_aria: "Close panel",
    resize_aria: "Resize profile panel",
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
    message_button: "Message",
    message_opening: "Opening…",
    display_name_label: "Display name",
    description_label: "Description",
    info_section: "Info",
    role_label: "Role",
    role_agent: "Agent",
    actions_section: "Actions",
  },
  inspector: {
    section_properties: "Properties",
    prop_runtime: "Runtime",
    prop_model: "Model",
    prop_visibility: "Visibility",
    display_name_title: "Edit display name",
    display_name_placeholder: "Agent display name",
    display_name_required: "Display name is required",
    save: "Save",
    cancel: "Cancel",
  },
  execution_config: {
    applies_next_run: "Changes take effect on the next run",
  },
  row: {
    archived: "Archived",
  },
};

// Extracted to a named const so the spreads below start from a concrete
// `MemberWithUser`. Under `noUncheckedIndexedAccess`, `members[0]` is
// `MemberWithUser | undefined`, and spreading that into a `: MemberWithUser`
// literal drops the spread-only required fields (workspace_id/role/…) — TS2322.
const ownerMember: MemberWithUser = {
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
};

const members: MemberWithUser[] = [ownerMember];

function makeAgent(
  ownerId = "user-owner",
  managedRole?: "group_manager",
  visibility: Agent["visibility"] = "workspace",
): Agent {
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
    visibility,
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
    mockRuntimes.current = [];
  });

  it("shows the staged (ready_to_apply) presentation for the agent's runtime (#687)", () => {
    // Staged projection: backend still reports health update_available, but the
    // panel must present the ready_to_apply lifecycle via the shared derive
    // (the label mock echoes the presentation it was handed).
    mockRuntimes.current = [
      { id: "runtime-1", status: "online", runtime_health: "update_available", update_state: "ready_to_apply" },
    ];
    renderPanel();
    expect(screen.getByText("ready_to_apply")).toBeInTheDocument();
  });

  it("keeps non-owner access to profile only by default", () => {
    renderPanel("user-other");
    expect(screen.getAllByText("Atlas").length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "Activity" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Files" })).not.toBeInTheDocument();
  });

  it("header shows avatar badge only — no Online/Offline name-row text (LRM-248)", () => {
    renderPanel();
    expect(screen.getByTestId("agent-profile-identity")).toHaveTextContent("Atlas");
    expect(screen.getByTestId("agent-presence-overlay")).toBeInTheDocument();
    expect(screen.queryByText("Online")).toBeNull();
    expect(screen.queryByText("Offline")).toBeNull();
    expect(screen.queryByTestId("presence-status")).toBeNull();
    expect(screen.queryByTestId("agent-live-status")).toBeNull();
  });

  it("puts Message in vertical Actions — not between header and tabs (LRM-448)", () => {
    renderPanel();
    expect(screen.queryByTestId("agent-profile-message-button")).not.toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-actions")).toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-action-message")).toHaveTextContent("Message");
    expect(screen.queryByRole("button", { name: "More" })).not.toBeInTheDocument();
  });

  it("shows Usage as its own tab — not stacked in Profile (LRM-448)", () => {
    renderPanel("user-owner");
    expect(screen.getByRole("button", { name: "Usage" })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Usage" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Usage" }));
    expect(screen.getByRole("region", { name: "Usage" })).toBeInTheDocument();
    expect(screen.getByText(/Last 30 days · reported usage only/)).toBeInTheDocument();
    expect(screen.getByText("No reported usage yet")).toBeInTheDocument();
  });

  it("temporarily exposes Activity, but not Files, to a workspace-member viewer", () => {
    const workspaceMember: MemberWithUser = {
      ...ownerMember,
      id: "m-viewer",
      user_id: "user-other",
      name: "Viewer",
      display_name: "Viewer",
      email: "viewer@example.com",
    };

    render(
      <AgentSidePanel
        agent={makeAgent()}
        currentUserId="user-other"
        members={[...members, workspaceMember]}
        onClose={() => {}}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Activity" }));
    expect(screen.getByText("Activity content")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Files" })).not.toBeInTheDocument();
  });

  it("does not advertise Activity to a non-owner of a private agent", () => {
    const workspaceMember: MemberWithUser = {
      ...ownerMember,
      id: "m-viewer",
      user_id: "user-other",
      name: "Viewer",
      display_name: "Viewer",
      email: "viewer@example.com",
    };

    render(
      <AgentSidePanel
        agent={makeAgent("user-owner", undefined, "private")}
        currentUserId="user-other"
        members={[...members, workspaceMember]}
        onClose={() => {}}
      />,
    );

    expect(screen.queryByRole("button", { name: "Activity" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Files" })).not.toBeInTheDocument();
  });

  // #656 — Reminders reuses the exact same visibility gate as Activity per
  // the V2 spec, and must always render as a direct tab (this panel has no
  // "More" overflow menu to hide it behind).
  it("shows Reminders as a direct tab to a workspace-member viewer, same gate as Activity", () => {
    const workspaceMember: MemberWithUser = {
      ...ownerMember,
      id: "m-viewer",
      user_id: "user-other",
      name: "Viewer",
      display_name: "Viewer",
      email: "viewer@example.com",
    };

    render(
      <AgentSidePanel
        agent={makeAgent()}
        currentUserId="user-other"
        members={[...members, workspaceMember]}
        onClose={() => {}}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Reminders" }));
    expect(screen.getByText("Reminders content")).toBeInTheDocument();
  });

  it("does not advertise Reminders to a non-owner of a private agent", () => {
    const workspaceMember: MemberWithUser = {
      ...ownerMember,
      id: "m-viewer",
      user_id: "user-other",
      name: "Viewer",
      display_name: "Viewer",
      email: "viewer@example.com",
    };

    render(
      <AgentSidePanel
        agent={makeAgent("user-owner", undefined, "private")}
        currentUserId="user-other"
        members={[...members, workspaceMember]}
        onClose={() => {}}
      />,
    );

    expect(screen.queryByRole("button", { name: "Reminders" })).not.toBeInTheDocument();
  });

  it("shows Profile, Activity, Reminders, Files, and Usage as direct tabs for the owner — none hidden behind a 'More' menu", () => {
    renderPanel("user-owner");

    expect(screen.getByRole("button", { name: "Profile" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Activity" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reminders" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Files" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Usage" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "More" })).not.toBeInTheDocument();
  });

  it("shows a standalone reported-usage card on the Usage tab instead of a fake session-token baseline", () => {
    renderPanel("user-owner");
    fireEvent.click(screen.getByRole("button", { name: "Usage" }));
    expect(screen.getByRole("region", { name: "Usage" })).toBeInTheDocument();
    expect(screen.getByText(/Last 30 days · reported usage only/)).toBeInTheDocument();
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
    fireEvent.click(screen.getByRole("button", { name: "Usage" }));

    expect(screen.getByText("Estimated cost")).toBeInTheDocument();
    expect(screen.getByText("$3.00")).toHaveClass("text-sm", "tabular-nums");
    expect(screen.getByText("Tokens")).toBeInTheDocument();
    expect(screen.getByText("1M")).toHaveClass("text-sm", "tabular-nums", "text-muted-foreground");
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
    fireEvent.click(screen.getByRole("button", { name: "Usage" }));

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
    expect(container.querySelector("aside")).toHaveClass("min-w-0", "w-full");
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
    const { container } = renderPanel();

    expect(container.querySelector("aside")).toHaveClass("w-full", "min-w-0");
    const activityTab = screen.getByRole("button", { name: "Activity" });
    expect(activityTab).toHaveClass("shrink-0", "px-3");
    expect(activityTab).not.toHaveClass("flex-1", "justify-center", "min-h-11");
    expect(activityTab.parentElement).not.toHaveClass("w-full", "px-0");
  });

  it("never renders a separate Config tab; Runtime Config is its own Profile section (LRM-470)", () => {
    renderPanel("user-owner", "group_manager");
    expect(screen.queryByRole("button", { name: "Config" })).not.toBeInTheDocument();
    expect(screen.getByText("Info")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Runtime Config" })).toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-runtime-config")).toBeInTheDocument();
    expect(screen.queryByText("Properties")).not.toBeInTheDocument();
    expect(screen.getByTestId("runtime-picker")).toBeInTheDocument();
    expect(screen.getByTestId("visibility-picker")).toBeInTheDocument();
    expect(screen.queryByTestId("concurrency-picker")).not.toBeInTheDocument();
    expect(screen.getByText("Changes take effect on the next run")).toBeInTheDocument();
  });

  it("LRM-448: Actions stack + Info field labels; Usage lives on its tab", () => {
    renderPanel("user-owner");
    expect(screen.getByTestId("agent-profile-actions")).toBeInTheDocument();
    expect(screen.getByText("Display name")).toBeInTheDocument();
    expect(screen.getByText("Description")).toBeInTheDocument();
    expect(screen.getByText("Info")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Usage" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Usage" }));
    const usage = screen.getByRole("region", { name: "Usage" });
    expect(usage.querySelector("h3")?.className).toMatch(/text-muted-foreground/);
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
    // Visibility stays editable too (LRM-387: Frank — must support modify).
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
