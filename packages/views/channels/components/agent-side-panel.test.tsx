// @vitest-environment jsdom

import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { configStore } from "@multica/core/config";
import enAgents from "../../locales/en/agents.json";
import { AgentSidePanel } from "./agent-side-panel";

const filesPanelProps = vi.fn();

const openDMMocks = vi.hoisted(() => ({
  openDM: vi.fn(),
  isPending: false,
}));

// Per-test permission decisions. Both come from `useAgentPermissions`, which is
// stubbed here: these tests assert that the panel *wires* a decision to the
// right surface. Whether the decision itself is correct — owner, workspace
// admin — is covered by `canViewAgentSensitiveTabs` / `canEditAgent`
// in packages/core/permissions/rules.test.ts, against the same context the
// backend gates on. Defaulting both to denied keeps the read-only paths honest.
const {
  permission,
  activityPermission,
  rolePermission,
  usageRows,
  mockRuntimes,
  mockRuntimeConfig,
  mockLocalSkills,
  mockWorkspaceSkills,
  updateAgentWorkspaceRole,
  setQueryData,
  invalidateQueries,
  roleDialogProps,
} = vi.hoisted(() => ({
  permission: { allowed: false },
  activityPermission: { allowed: false },
  rolePermission: { allowed: false },
  updateAgentWorkspaceRole: vi.fn(
    async (_workspaceId: string, _agentId: string, _role: "member" | "admin") => ({ status: "ok" }),
  ),
  setQueryData: vi.fn(),
  invalidateQueries: vi.fn(),
  roleDialogProps: vi.fn(),
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
  // What GET /api/agents/{id}/runtime-config returns: Computer + runtime
  // assembled server-side, independent of what the runtimes list carries.
  mockRuntimeConfig: {
    current: null as null | Record<string, unknown>,
  },
  mockLocalSkills: { current: [] as Array<Record<string, unknown>> },
  mockWorkspaceSkills: { current: [] as Array<Record<string, unknown>> },
}));

// The Info grid renders <Time kind="date"/>, which reads the viewer timezone
// off the auth store. Nothing here depends on a signed-in user.
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: null }) => unknown) => sel({ user: null }),
  registerAuthStore: vi.fn(),
}));

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: () => null,
}));

vi.mock("../../common/actor-avatar", () => ({
  AgentPresenceOverlay: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="agent-presence-overlay">{children}</div>
  ),
  ActorAvatar: ({ name }: { name?: string }) => (
    <div data-testid="owner-avatar" aria-label={name} />
  ),
}));

vi.mock("../../agents/components/tabs/activity-tab", () => ({
  ActivityTab: () => <div>Activity content</div>,
}));

vi.mock("../../agents/components/agent-activity-list-item", () => ({
  AgentActivityStatus: ({ testId }: { testId?: string }) => (
    <div data-testid={testId ?? "agent-activity-status"}>Online</div>
  ),
}));

vi.mock("../../agents/components/agent-honor-panel-section", () => ({
  AgentHonorPanelSection: () => (
    <div data-testid="agent-honor-panel-section">Honor</div>
  ),
}));

vi.mock("../../agents/components/tabs/reminders-tab", () => ({
  RemindersTab: () => <div>Reminders content</div>,
}));

vi.mock("../../agents/components/agent-profile-actions", () => ({
  AgentProfileActions: ({
    canManage,
    layout = "stack",
  }: {
    canManage: boolean;
    layout?: "stack" | "icons";
  }) => (
    <div
      data-testid={
        layout === "icons" ? "agent-profile-chrome-actions" : "agent-profile-actions"
      }
      data-can-manage={String(canManage)}
    >
      {layout === "icons" ? (
        <button type="button" data-testid="agent-profile-chrome-action-message" />
      ) : (
        <button type="button" data-testid="agent-profile-action-delete">
          Delete
        </button>
      )}
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

// LRM-542: the header avatar editor has its own test file. Stub it here so the
// panel test stays focused on header structure + permission gating, and so the
// editor's heavy deps (useFileUpload / canvas crop / query client) don't leak in.
vi.mock("../../agents/components/agent-profile-avatar-editor", () => ({
  AgentProfileAvatarEditor: ({ canEdit }: { canEdit: boolean }) => (
    <div
      data-testid="agent-profile-avatar"
      data-can-edit={String(canEdit)}
    />
  ),
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
  RuntimePicker: (p: { canEdit?: boolean; selectedProvider?: string | null }) => (
    <div
      data-testid="runtime-picker"
      data-can-edit={String(!!p.canEdit)}
      data-selected-provider={p.selectedProvider ?? ""}
    />
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
// Role has its own test file (agent-workspace-role.test.tsx) covering the
// picker and its PATCH. Stubbed here so this file's hand-rolled resource
// object does not have to carry the whole workspace_role i18n namespace.
vi.mock("../../agents/components/agent-workspace-role", () => ({
  AgentWorkspaceRole: (p: { agent?: { workspace_role?: string } }) => (
    <div data-testid="agent-workspace-role-value">
      {p.agent?.workspace_role === "admin" ? "Admin" : "Member"}
    </div>
  ),
}));
vi.mock("../../agents/components/runtime-config-dialog", () => ({
  RuntimeConfigDialog: (p: { open: boolean }) =>
    p.open ? <div data-testid="agent-runtime-config-dialog" /> : null,
}));
vi.mock("../../common/prop-row", () => ({
  PropRow: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));
vi.mock("../../agents/hooks/use-update-agent", () => ({
  useUpdateAgent: () => vi.fn(),
}));
vi.mock("../../settings/components/roles-dialog", () => ({
  RolesDialog: (props: {
    open: boolean;
    onSave?: (role: "member" | "admin" | "owner") => Promise<void> | void;
  }) => {
    roleDialogProps(props);
    return props.open ? (
      <button type="button" data-testid="agent-workspace-role-save" onClick={() => void props.onSave?.("admin")}>
        Save role
      </button>
    ) : null;
  },
}));
vi.mock("../../common/use-open-dm", () => ({
  useOpenDM: () => openDMMocks,
}));
vi.mock("@multica/core/api", () => ({
  api: {
    updateAgentWorkspaceRole: (
      workspaceId: string,
      agentId: string,
      role: "member" | "admin",
    ) => updateAgentWorkspaceRole(workspaceId, agentId, role),
  },
}));
vi.mock("@multica/core/permissions", () => ({
  useAgentPermissions: () => ({
    canEdit: permission,
    canChangeRole: rolePermission,
    canViewSensitiveTabs: activityPermission,
  }),
}));
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getAgentFleetRank: () => undefined,
    getMemberHonor: () => undefined,
  }),
}));
vi.mock("@tanstack/react-query", () => ({
  queryOptions: (options: unknown) => options,
  useQuery: (options: { kind?: string; queryKey?: unknown[] }) => ({
    data:
      options.kind === "usage-by-agent"
        ? usageRows
        : options.queryKey?.[0] === "agents" && options.queryKey?.[1] === "profile-skills"
          ? { global: mockLocalSkills.current, workspace: mockWorkspaceSkills.current }
          : options.queryKey?.[0] === "runtimes" && options.queryKey?.[1] === "agent-config"
            ? mockRuntimeConfig.current
          : options.queryKey?.[0] === "runtimes"
            ? mockRuntimes.current
          : [],
    isLoading: false,
  }),
  useQueryClient: () => ({ setQueryData, invalidateQueries }),
}));
vi.mock("@multica/core/runtimes", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"] }),
  agentRuntimeConfigOptions: () => ({ queryKey: ["runtimes", "agent-config"] }),
  agentProfileSkillsOptions: () => ({ queryKey: ["agents", "profile-skills"] }),
  deriveRuntimeHealth: (rt: { status?: string }) =>
    rt?.status === "online" ? "online" : "offline",
  runtimeCurrentVersion: (rt: { current_version?: string | null }) =>
    rt?.current_version?.trim() || null,
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
    files: "Workspace",
    usage: "Usage",
    config: "Config",
  },
  side_panel: {
    close_aria: "Close panel",
    back_to_member_aria: "Back to {{name}}",
    back_to_messages: "Back to messages",
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
    skills_section: "Skills",
    global_skills: "Global skills",
    workspace_skills: "Workspace skills",
    no_global_skills: "No global skills discovered",
    no_workspace_skills: "No workspace skills configured",
    actions_section: "Actions",
  },
  profile_card: {
    role_label: "Role",
    role_dialog_title: "Agent role",
    role_dialog_subtitle: "Choose this agent's workspace role",
    role_updated: "Role updated",
    role_update_failed: "Failed to update role",
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
    // Import real keys — do not hand-copy (Parker: mock drift).
    prop_computer: enAgents.inspector.prop_computer,
    computer_connected: enAgents.inspector.computer_connected,
    computer_disconnected: enAgents.inspector.computer_disconnected,
    computer_version: enAgents.inspector.computer_version,
    computer_none: enAgents.inspector.computer_none,
  },
  runtime_config: {
    applies_next_run: "Changes take effect on the next run",
    dialog_title: "Runtime config",
    dialog_description: "Edits stay local until you save.",
    dialog_saving: "Saving…",
    edit_trigger_aria: "Edit runtime, model, and thinking",
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
  description: "",
  created_at: "2026-01-01T00:00:00Z",
};

const members: MemberWithUser[] = [ownerMember];

function makeAgent(ownerId = "user-owner"): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    workspace_role: "member",
    runtime_id: "runtime-1",
    name: "atlas",
    display_name: "Atlas",
    description: "Coordinates project context",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    status: "idle",
    model: "",
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
  variant?: "page",
) {
  return render(
    <AgentSidePanel
      agent={makeAgent("user-owner")}
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
    activityPermission.allowed = false;
    rolePermission.allowed = false;
    usageRows.length = 0;
    mockRuntimes.current = [];
    mockRuntimeConfig.current = null;
    mockLocalSkills.current = [];
    mockWorkspaceSkills.current = [];
  });

  it("shows global and workspace skills separately in the profile", () => {
    mockLocalSkills.current = [
      {
        name: "global-review",
        description: "Shared review rules",
        path: "~/.agents/skills/global-review",
      },
    ];
    mockWorkspaceSkills.current = [{ name: "deploy", description: "Deploy safely", path: "agent/skills/deploy" }];
    const agent = { ...makeAgent("user-owner"), skills: [{ id: "skill-1", name: "deploy", description: "Deploy safely" }] };
    render(
      <AgentSidePanel
        agent={agent}
        currentUserId="user-owner"
        members={members}
        onClose={() => {}}
      />,
    );

    const skills = screen.getByTestId("agent-profile-skills");
    expect(within(skills).getByText(/Global skills/)).toBeInTheDocument();
    expect(within(skills).getByText("global-review")).toBeInTheDocument();
    expect(within(skills).getByText(/Workspace skills/)).toBeInTheDocument();
    expect(within(skills).getByText("deploy")).toBeInTheDocument();
  });

  it("renders no health or update status beside Runtime", () => {
    mockRuntimes.current = [
      { id: "runtime-1", status: "online", runtime_health: "update_available", update_state: "ready_to_apply" },
    ];
    renderPanel();
    expect(screen.queryByText("ready_to_apply")).not.toBeInTheDocument();
    expect(screen.queryByText("update_available")).not.toBeInTheDocument();
  });

  // task #28 + Computer-first: computer binding lives in Runtime config
  // (not the Info section) so Computer → Runtime → Model stay together.
  it("shows the bound computer's connection + label in Runtime config (#28)", () => {
    mockRuntimeConfig.current = {
      computer: { daemon_id: "daemon-1", name: "s144", connected: true },
      runtime: { id: "runtime-1", provider: "cursor" },
    };
    renderPanel();
    const runtimeSection = screen.getByTestId("agent-profile-runtime-config");
    expect(within(runtimeSection).getByText("Computer")).toBeInTheDocument();
    expect(within(runtimeSection).getByText("s144")).toBeInTheDocument();
    // Online: the dot carries the state visually, the text is screen-reader only.
    expect(within(runtimeSection).getByText("Connected")).toHaveClass("sr-only");
  });

  it("shows disconnected when the Computer has no live runner socket (#28)", () => {
    mockRuntimeConfig.current = {
      computer: { daemon_id: "daemon-1", name: "s144", connected: false },
      runtime: { id: "runtime-1", provider: "cursor" },
    };
    renderPanel();
    const runtimeSection = screen.getByTestId("agent-profile-runtime-config");
    // Grey dot carries it visually; the word exists only for screen readers.
    expect(within(runtimeSection).getByText("Disconnected")).toHaveClass("sr-only");
    expect(within(runtimeSection).getByText("s144")).toBeInTheDocument();
  });

  // Frank, 2026-08-21: this is the case that used to read "No computer" for
  // everyone but the owner. The runtimes list is "what may I bind to" and
  // never carries another member's private runtime; the assembled config
  // answers "where does this agent run", which is a different question.
  it("names the computer even when the runtimes list has nothing to bind to", () => {
    mockRuntimes.current = [];
    mockRuntimeConfig.current = {
      computer: { daemon_id: "daemon-1", name: "s144", connected: true },
      runtime: { id: "runtime-1", provider: "cursor" },
    };
    renderPanel();
    const runtimeSection = screen.getByTestId("agent-profile-runtime-config");
    expect(within(runtimeSection).queryByText("No computer")).not.toBeInTheDocument();
    expect(within(runtimeSection).getByText("s144")).toBeInTheDocument();
    // The picker is still handed only what the viewer may bind to.
    expect(within(runtimeSection).getByTestId("runtime-picker")).toHaveAttribute(
      "data-selected-provider",
      "cursor",
    );
  });

  it("shows the no-computer fallback when the agent has no bound computer (#28)", () => {
    mockRuntimes.current = [];
    mockRuntimeConfig.current = { computer: null, runtime: null };
    renderPanel();
    const runtimeSection = screen.getByTestId("agent-profile-runtime-config");
    expect(within(runtimeSection).getByText("No computer")).toBeInTheDocument();
  });

  it("shows the agent honor summary on the profile tab", () => {
    renderPanel();
    expect(screen.getByTestId("agent-honor-panel-section")).toBeInTheDocument();
  });

  it("keeps non-owner access to profile only by default", () => {
    renderPanel("user-other");
    expect(screen.getAllByText("Atlas").length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "Activity" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Workspace" })).not.toBeInTheDocument();
  });

  it("shows the owner avatar alongside the owner name", () => {
    renderPanel();
    expect(screen.getByTestId("owner-avatar")).toHaveAttribute("aria-label", "Owner");
    expect(screen.getAllByText("Owner").length).toBeGreaterThanOrEqual(2);
  });

  it("shows the current dynamic status under the agent handle", () => {
    renderPanel();
    expect(screen.getByTestId("agent-profile-identity")).toHaveTextContent("Atlas");
    expect(screen.getByTestId("agent-profile-avatar")).toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-current-status")).toHaveTextContent("Online");
    expect(screen.queryByTestId("presence-status")).toBeNull();
    expect(screen.queryByTestId("agent-live-status")).toBeNull();
  });

  it("LRM-542: renders a floating close button + tightened identity row (no separate close bar)", () => {
    renderPanel();
    // × floats top-right instead of living in its own bordered header row.
    expect(screen.getByRole("button", { name: "Close panel" })).toBeInTheDocument();
    const identity = screen.getByTestId("agent-profile-identity");
    // Avatar + name share one centered row; top padding tightened to ~14px.
    expect(identity).toHaveClass("items-center", "pt-3.5");
  });

  it("LRM-877: stacked Agent chrome shows ← {member} pop + Close clears via onClose", () => {
    const onBack = vi.fn();
    const onClose = vi.fn();
    render(
      <AgentSidePanel
        agent={makeAgent("user-owner")}
        currentUserId="user-owner"
        members={members}
        onClose={onClose}
        onBack={onBack}
        backLabel="Frank An"
      />,
    );
    fireEvent.click(screen.getByTestId("agent-panel-back-to-member"));
    expect(onBack).toHaveBeenCalledTimes(1);
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Close panel" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("LRM-542: renders the avatar read-only when the edit permission is denied", () => {
    permission.allowed = false;
    renderPanel("user-owner");
    expect(screen.getByTestId("agent-profile-avatar")).toHaveAttribute(
      "data-can-edit",
      "false",
    );
  });

  it("LRM-542: enables the header avatar editor when the edit permission is granted", () => {
    permission.allowed = true;
    renderPanel("user-owner");
    expect(screen.getByTestId("agent-profile-avatar")).toHaveAttribute(
      "data-can-edit",
      "true",
    );
  });

  it("puts the actions menu in chrome and leaves only Delete in the Profile body", () => {
    renderPanel();
    expect(screen.queryByTestId("agent-profile-message-button")).not.toBeInTheDocument();
    const chrome = screen.getByTestId("agent-profile-chrome-actions");
    const actions = screen.getByTestId("agent-profile-actions");
    expect(screen.getByTestId("agent-profile-tab-content")).toContainElement(actions);
    expect(screen.getByTestId("agent-profile-identity")).not.toContainElement(actions);
    expect(screen.getByTestId("agent-profile-identity")).not.toContainElement(chrome);
    expect(screen.queryByTestId("agent-profile-action-message")).not.toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-action-delete")).toHaveTextContent("Delete");
    expect(screen.getByTestId("agent-profile-chrome-action-message")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "More" })).not.toBeInTheDocument();
  });

  // The role control itself is AgentWorkspaceRole, stubbed above and covered
  // by its own test file — including that selecting a role PATCHes directly,
  // with no dialog in between. What matters here is that Info renders it.
  it("shows an agent's workspace role in Info", () => {
    renderPanel();
    expect(screen.getByTestId("agent-workspace-role-value")).toHaveTextContent("Member");
    expect(screen.queryByText("Agent")).not.toBeInTheDocument();
    // The old pencil-into-a-modal entry point is gone: one value, one picker,
    // the same one the detail inspector renders.
    expect(screen.queryByTestId("agent-workspace-role-edit")).not.toBeInTheDocument();
  });

  it("shows Usage as its own tab — not stacked in Profile (LRM-448)", () => {
    // Usage now shares Activity's gate (Frank: "其余几个 tab 同理"), so an
    // allowed decision is a precondition of this test, not its subject.
    activityPermission.allowed = true;
    renderPanel("user-owner");
    expect(screen.getByRole("button", { name: "Usage" })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Usage" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Usage" }));
    expect(screen.getByRole("region", { name: "Usage" })).toBeInTheDocument();
    expect(screen.getByText(/Last 30 days · reported usage only/)).toBeInTheDocument();
    expect(screen.getByText("No reported usage yet")).toBeInTheDocument();
  });

  it("exposes Activity AND Files under one decision (Frank: 其余几个 tab 同理)", () => {
    activityPermission.allowed = true;
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
    // Files used to require a separate, stricter condition. One gate now covers
    // Activity / Reminders / Files / Usage — a split here would be the bug.
    expect(screen.getByRole("button", { name: "Workspace" })).toBeInTheDocument();
  });

  it("does not advertise Activity when the activity decision denies", () => {
    // activityPermission stays denied — this is the private-agent, non-owner,
    // non-admin case that canViewAgentActivity rejects.
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
        agent={makeAgent("user-owner")}
        currentUserId="user-other"
        members={[...members, workspaceMember]}
        onClose={() => {}}
      />,
    );

    expect(screen.queryByRole("button", { name: "Activity" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Workspace" })).not.toBeInTheDocument();
  });

  // #656 — Reminders reuses the exact same visibility gate as Activity per
  // the V2 spec, and must always render as a direct tab (this panel has no
  // "More" overflow menu to hide it behind).
  it("shows Reminders as a direct tab whenever Activity is allowed, same gate", () => {
    activityPermission.allowed = true;
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
        agent={makeAgent("user-owner")}
        currentUserId="user-other"
        members={[...members, workspaceMember]}
        onClose={() => {}}
      />,
    );

    expect(screen.queryByRole("button", { name: "Reminders" })).not.toBeInTheDocument();
  });

  it("shows Profile, Activity, Reminders, Files, and Usage as direct tabs for the owner — none hidden behind a 'More' menu", () => {
    activityPermission.allowed = true;
    renderPanel("user-owner");

    expect(screen.getByRole("button", { name: "Profile" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Activity" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reminders" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Workspace" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Usage" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "More" })).not.toBeInTheDocument();
  });

  it("shows a standalone reported-usage card on the Usage tab instead of a fake session-token baseline", () => {
    activityPermission.allowed = true;
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

    activityPermission.allowed = true;
    renderPanel("user-owner");
    fireEvent.click(screen.getByRole("button", { name: "Usage" }));

    expect(screen.getByText("Estimated cost")).toBeInTheDocument();
    expect(screen.getByText("$3.00")).toHaveClass("text-sm", "tabular-nums");
    expect(screen.getByText("Tokens")).toBeInTheDocument();
    expect(screen.getByText("1M")).toHaveClass("text-sm", "tabular-nums", "text-muted-foreground");
  });

  it("does not invent a cost when a reported model has no pricing", () => {
    activityPermission.allowed = true;
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

  it("keeps visited page tabs mounted in equal-width 44px mobile targets", () => {
    activityPermission.allowed = true;
    const { container } = renderPanel("user-owner", "page");

    // LRM-1185 (父 LRM-974 冻 A1): the page variant used to render an EMPTY
    // dismiss slot — that is the bug Frank reported ("详情卡没有关闭按钮"), so the
    // floating close must now exist with a 44×44 hit target even on `page`.
    const pageClose = screen.getByTestId("side-panel-page-close");
    expect(pageClose).toHaveClass("size-8");
    expect(pageClose).toHaveAccessibleName("Close panel");
    expect(container.querySelector("aside")).toHaveClass("min-w-0", "w-full");
    expect(container.querySelector(".overflow-y-auto")).toHaveClass("min-w-0");
    for (const tab of ["Profile", "Activity", "Reminders", "Workspace"]) {
      expect(screen.getByRole("button", { name: tab })).toHaveClass(
        "min-h-11",
        "flex-1",
        "justify-center",
      );
    }
    expect(screen.getByRole("button", { name: "Activity" }).parentElement).toHaveClass("w-full", "px-0");

    fireEvent.click(screen.getByRole("button", { name: "Activity" }));
    expect(screen.getByText("Activity content")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Reminders" }));
    expect(screen.getByText("Reminders content")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Profile" }));
    expect(screen.getByText("Activity content")).toBeInTheDocument();
    expect(screen.getByText("Reminders content")).toBeInTheDocument();
  });

  it("restores a visited page tab's scroll position after switching tabs", () => {
    activityPermission.allowed = true;
    const { container } = renderPanel("user-owner", "page");
    const tabBody = container.querySelector(".overflow-y-auto") as HTMLDivElement;

    fireEvent.click(screen.getByRole("button", { name: "Activity" }));
    tabBody.scrollTop = 128;

    fireEvent.click(screen.getByRole("button", { name: "Profile" }));
    tabBody.scrollTop = 24;

    fireEvent.click(screen.getByRole("button", { name: "Activity" }));
    expect(tabBody.scrollTop).toBe(128);
  });

  it("keeps desktop panel tabs content-width and left aligned", () => {
    activityPermission.allowed = true;
    const { container } = renderPanel();

    expect(container.querySelector("aside")).toHaveClass("w-full", "min-w-0");
    const activityTab = screen.getByRole("button", { name: "Activity" });
    expect(activityTab).toHaveClass("shrink-0", "px-3");
    expect(activityTab).not.toHaveClass("flex-1", "justify-center", "min-h-11");
    expect(activityTab.parentElement).not.toHaveClass("w-full", "px-0");
  });

  it("never renders a separate Config tab; Runtime Config is its own Profile section (LRM-470)", () => {
    activityPermission.allowed = true;
    permission.allowed = true;
    renderPanel("user-owner");
    expect(screen.queryByRole("button", { name: "Config" })).not.toBeInTheDocument();
    expect(screen.getByText("Info")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Runtime Config" })).toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-runtime-config")).toBeInTheDocument();
    expect(screen.queryByText("Properties")).not.toBeInTheDocument();
    expect(screen.getByTestId("runtime-picker")).toBeInTheDocument();
    expect(screen.queryByTestId("concurrency-picker")).not.toBeInTheDocument();
    expect(screen.queryByText("Changes take effect on the next run")).not.toBeInTheDocument();
  });

  it("keeps header actions visible while Info and Usage stay in their own surfaces", () => {
    activityPermission.allowed = true;
    renderPanel("user-owner");
    expect(screen.getByTestId("agent-profile-chrome-actions")).toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-actions")).toBeInTheDocument();
    expect(screen.getByText("Display name")).toBeInTheDocument();
    expect(screen.getByText("Description")).toBeInTheDocument();
    expect(screen.getByText("Info")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Usage" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Usage" }));
    const usage = screen.getByRole("region", { name: "Usage" });
    expect(usage.querySelector("h3")?.className).toMatch(/text-muted-foreground/);
  });

  it("renders exactly the 3 runtime pickers, no Concurrency (#565 fix-forward)", () => {
    activityPermission.allowed = true;
    renderPanel("user-owner");
    for (const id of ["runtime-picker", "model-picker", "thinking-picker"]) {
      expect(screen.getByTestId(id)).toBeInTheDocument();
    }
    expect(screen.queryByTestId("concurrency-picker")).not.toBeInTheDocument();
  });

  it("LRM-1351: heading pencil opens Dialog; summary body is not a click target", () => {
    permission.allowed = true;
    renderPanel("user-other");

    for (const id of ["runtime-picker", "model-picker", "thinking-picker"]) {
      expect(screen.getByTestId(id)).toHaveAttribute("data-can-edit", "false");
    }
    expect(screen.queryByTestId("agent-runtime-config-dialog")).not.toBeInTheDocument();

    // Summary chips must not wrap a row-wide edit button (Frank pencil lock).
    const edit = screen.getByTestId("agent-runtime-config-edit");
    expect(edit).toHaveAttribute(
      "aria-label",
      "Edit runtime, model, and thinking",
    );
    expect(edit.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
    expect(edit.closest("[data-testid='agent-profile-runtime-config']")).toBeTruthy();
    expect(edit.parentElement).toContainElement(
      screen.getByRole("heading", { name: "Runtime Config" }),
    );
    expect(screen.getByTestId("runtime-picker").closest("button")).not.toBe(edit);

    fireEvent.click(screen.getByTestId("runtime-picker"));
    expect(screen.queryByTestId("agent-runtime-config-dialog")).not.toBeInTheDocument();

    fireEvent.click(edit);
    expect(screen.getByTestId("agent-runtime-config-dialog")).toBeInTheDocument();
  });

  it("renders READ-ONLY runtime pickers in Profile for a non-owner, non-group-manager", () => {
    permission.allowed = false;
    renderPanel("user-other");

    for (const id of ["runtime-picker", "model-picker", "thinking-picker"]) {
      expect(screen.getByTestId(id)).toHaveAttribute("data-can-edit", "false");
    }
    expect(screen.queryByTestId("agent-runtime-config-edit")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Edit runtime, model, and thinking" }),
    ).not.toBeInTheDocument();
  });

  it("threads the owner/admin permission decision into the runtime edit affordance", () => {
    // For an ordinary agent, editability comes straight from useAgentPermissions.
    permission.allowed = true;
    renderPanel("user-owner");

    expect(screen.getByTestId("agent-runtime-config-edit")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Edit runtime, model, and thinking" }),
    ).toBeInTheDocument();
    expect(screen.getByTestId("runtime-picker")).toHaveAttribute("data-can-edit", "false");
  });
});
