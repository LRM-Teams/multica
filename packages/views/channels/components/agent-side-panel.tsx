"use client";

import { type ReactNode, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Activity, ArrowLeft, BarChart3, Bell, FileText, Pencil, User } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AGENT_DESCRIPTION_MAX_LENGTH, agentDetailKeys } from "@multica/core/agents";
import { api } from "@multica/core/api";
import type { Agent, DashboardUsageByAgent, MemberWithUser } from "@multica/core/types";
import { deriveRuntimeHealth, deriveRuntimeHealthPresentation, runtimeListOptions, type RuntimeHealthPresentation } from "@multica/core/runtimes";
import { useAgentPermissions } from "@multica/core/permissions";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
import {
  formatActorHandleLabel,
  resolveActorDisplayName,
  resolveActorHandle,
} from "@multica/core/identity";
import { dashboardUsageByAgentOptions } from "@multica/core/dashboard/queries";
import { useCustomPricingStore } from "@multica/core/runtimes/custom-pricing-store";
import { cn } from "@multica/ui/lib/utils";
import { AgentHonorPanelSection } from "../../agents/components/agent-honor-panel-section";
import { AgentActivityStatus } from "../../agents/components/agent-activity-list-item";
import { ActivityTab } from "../../agents/components/tabs/activity-tab";
import { RemindersTab } from "../../agents/components/tabs/reminders-tab";
import { AgentProfileAvatarEditor } from "../../agents/components/agent-profile-avatar-editor";
import { ModelPicker } from "../../agents/components/inspector/model-picker";
import { RuntimePicker } from "../../agents/components/inspector/runtime-picker";
import { ComputerInfoRow } from "../../agents/components/inspector/computer-info-row";
import { ThinkingPropRow } from "../../agents/components/inspector/thinking-prop-row";
import { RuntimeConfigDialog } from "../../agents/components/runtime-config-dialog";
import { MemoryGrowthField } from "../../agents/components/memory-growth-field";
import { AgentProfileActions } from "../../agents/components/agent-profile-actions";
import { InlineFieldEditor } from "../../agents/components/inline-field-editor";
import { useUpdateAgent } from "../../agents/hooks/use-update-agent";
import { useRuntimeHealthStateLabel } from "../../runtimes/components/shared";
import { RolesDialog } from "../../settings/components/roles-dialog";
import { ConversationSidePanelShell } from "../../common/conversation-side-panel-shell";
import { ActorStyledName } from "../../common/actor-styled-name";
import { AgentFilesPanel } from "./agent-files-panel";
import { useT } from "../../i18n/use-t";
import { estimateCost, formatTokens, isModelPriced } from "../../runtimes/utils";

type OwnerTab = "activity" | "profile" | "reminders" | "files" | "usage";

const TAB_ICONS: Record<OwnerTab, typeof Activity> = {
  profile: User,
  activity: Activity,
  reminders: Bell,
  files: FileText,
  usage: BarChart3,
};

interface AgentSidePanelProps {
  agent: Agent;
  currentUserId: string | null;
  members: readonly MemberWithUser[];
  onClose: () => void;
  /** Mobile profile routes reuse this exact tab/body surface without dock chrome. */
  variant?: "panel" | "page";
  /**
   * LRM-877 Dock Stack — when set, chrome shows `← {backLabel}` to pop back to
   * the human Profile. ✕ / Done still call `onClose` (clear whole stack).
   */
  onBack?: () => void;
  backLabel?: string;
  /** Mobile page trailing control (「回消息」). Defaults to channels Done. */
  doneLabel?: string;
  hideDismiss?: boolean;
}

/**
 * Right-pane surface opened by clicking an agent's avatar/name in the
 * conversation — mutually exclusive with the thread panel (same slot,
 * per Frank's direction 2026-07-09: inline panel, not a route jump).
 *
 * LRM-448 Profile v4 (locked A): Computer IA + Multica tokens.
 * Header is Close-only (no Message+⋯). Identity sits under the chrome.
 * Profile tab: editable Display name / Description, Info, Runtime Config
 * section (LRM-470), vertical Actions. Usage is its own tab — never stacked
 * in Profile.
 */
export function AgentSidePanel({
  agent,
  currentUserId,
  members,
  onClose,
  variant = "panel",
  onBack,
  backLabel,
  doneLabel,
  hideDismiss = false,
}: AgentSidePanelProps) {
  const { t } = useT("agents");
  const isOwner = !!currentUserId && agent.owner_id === currentUserId;
  // LRM-542: the header avatar is now editable. Compute the edit permission
  // once here so the header avatar and the Profile tab share one decision.
  //
  // One gate for every sensitive tab — admin-or-owner, from the viewer's own
  // membership (Frank 2026-07-30: "其余几个 tab 同理"). Profile stays ungated:
  // it is "who is this agent", which everyone may see now that agent
  // visibility is retired.
  const { canEdit, canViewSensitiveTabs } = useAgentPermissions(agent, agent.workspace_id);
  const canViewSensitive = canViewSensitiveTabs.allowed;
  const availableTabs: OwnerTab[] = ["profile"];
  if (canViewSensitive) availableTabs.push("activity");
  if (canViewSensitive) availableTabs.push("reminders");
  if (canViewSensitive) availableTabs.push("files");
  // LRM-448: Usage is a direct tab, never stacked in Profile. It used to be
  // ungated — every member could read another agent's spend — so bringing it
  // under this gate is a *tightening*, unlike Files which was owner-only and
  // now admits admins.
  if (canViewSensitive) availableTabs.push("usage");
  const showTabBar = availableTabs.length > 1;
  const [tab, setTab] = useState<OwnerTab>("profile");
  const [mountedTabs, setMountedTabs] = useState<Set<OwnerTab>>(() => new Set(["profile"]));
  const tabBodyRef = useRef<HTMLDivElement>(null);
  const tabScrollTopRef = useRef<Partial<Record<OwnerTab, number>>>({});
  const displayName = resolveActorDisplayName(agent, agent.id);
  const handleLabel = formatActorHandleLabel(resolveActorHandle(agent));
  // LRM-542: header avatar is editable, so the outer panel needs the same
  // update handle the Profile tab already uses.
  const handleUpdate = useUpdateAgent(agent.workspace_id);
  const selectTab = (nextTab: OwnerTab) => {
    if (nextTab === tab) return;
    if (variant === "page" && tabBodyRef.current) {
      tabScrollTopRef.current[tab] = tabBodyRef.current.scrollTop;
    }
    setTab(nextTab);
    if (variant === "page") {
      setMountedTabs((current) =>
        current.has(nextTab) ? current : new Set([...current, nextTab]),
      );
    }
  };
  const renderTab = (tabId: OwnerTab) =>
    variant === "page" ? mountedTabs.has(tabId) : tab === tabId;

  useLayoutEffect(() => {
    if (variant !== "page" || !tabBodyRef.current) return;
    tabBodyRef.current.scrollTop = tabScrollTopRef.current[tab] ?? 0;
  }, [tab, variant]);

  // LRM-542: default chrome is a floating close — identity row IS the header.
  // LRM-877: when stacked on a human Profile, switch to a bar with `← {name}`
  // so pop-back is explicit; ✕ / Done still clears the whole stack via onClose.
  const stackedBack = !!onBack && !!backLabel?.trim();
  // Only inject 「回消息」 when stacked on a human Profile (or caller overrides).
  // Non-stacked page hosts (e.g. ActorProfilePage) keep their own Back chrome.
  const pageDoneLabel =
    doneLabel ??
    (variant === "page" && stackedBack
      ? t(($) => $.side_panel.back_to_messages)
      : undefined);

  const backName = stackedBack ? backLabel!.trim() : "";
  const leading = useMemo(() => {
    if (!stackedBack) return undefined;
    return (
      <button
        type="button"
        data-testid="agent-panel-back-to-member"
        onClick={onBack}
        className="inline-flex min-w-0 max-w-full items-center gap-1 rounded-md px-1.5 py-1 text-xs font-semibold text-brand transition-colors hover:bg-brand/10"
        aria-label={t(($) => $.side_panel.back_to_member_aria, {
          name: backName,
        })}
      >
        <ArrowLeft className="size-3.5 shrink-0" />
        <span className="truncate">{backName}</span>
      </button>
    );
  }, [stackedBack, onBack, backName, t]);

  return (
    <ConversationSidePanelShell
      variant={variant}
      header={stackedBack ? "bar" : "floating"}
      hideDismiss={hideDismiss}
      onClose={onClose}
      closeAriaLabel={t(($) => $.side_panel.close_aria)}
      doneLabel={pageDoneLabel}
      leading={leading}
    >
      <div
        className={cn(
          "flex shrink-0 items-center gap-3 pb-3 pl-4 pr-10 pt-3.5",
          // LRM-1185: the page floating close is now a real 44×44 hit target,
          // so the identity row must reserve 44 + inset instead of 40.
          variant === "page" && "pl-0 pr-14",
          stackedBack && "pr-4 pt-2",
        )}
        data-testid="agent-profile-identity"
      >
        <AgentProfileAvatarEditor
          agent={agent}
          canEdit={canEdit.allowed}
          onUpdate={handleUpdate}
        />
        <div className="min-w-0 flex-1">
          <ActorStyledName
            displayName={displayName}
            agentHonorLevel={agent.honor_level}
            honorSurface="profile"
            className="text-base font-bold leading-tight text-foreground"
          />
          <p className="mt-0.5 truncate text-xs text-muted-foreground">
            {handleLabel || `@${agent.name}`}
            {agent.archived_at ? (
              <span className="ml-2">{t(($) => $.row.archived)}</span>
            ) : null}
          </p>
          {!agent.archived_at ? (
            <AgentActivityStatus
              agentId={agent.id}
              className="mt-0.5 max-w-none"
              testId="agent-profile-current-status"
            />
          ) : null}
        </div>
      </div>

      {showTabBar ? (
        <>
          <div
            className={cn(
              "flex shrink-0 items-center gap-0 overflow-x-auto border-b px-2",
              variant === "page" && "w-full px-0",
            )}
          >
            {availableTabs.map((tabId) => {
              const Icon = TAB_ICONS[tabId];
              return (
                <button
                  key={tabId}
                  type="button"
                  onClick={() => selectTab(tabId)}
                  className={cn(
                    "flex items-center gap-1.5 whitespace-nowrap border-b-2 py-2.5 text-xs font-medium transition-colors",
                    variant === "page"
                      ? "min-h-11 min-w-0 flex-1 justify-center px-2"
                      : "shrink-0 px-3",
                    tab === tabId
                      ? "border-foreground text-foreground"
                      : "border-transparent text-muted-foreground hover:text-foreground",
                  )}
                >
                  <Icon className="size-3.5" />
                  {t(($) => $.tabs[tabId])}
                </button>
              );
            })}
          </div>
          <div ref={tabBodyRef} className="min-h-0 min-w-0 flex-1 overflow-y-auto">
            {renderTab("activity") && canViewSensitive ? (
              <div className={tab === "activity" ? undefined : "hidden"}>
                <ActivityTab agent={agent} />
              </div>
            ) : null}
            {renderTab("profile") ? (
              <div className={tab === "profile" ? undefined : "hidden"}>
                <AgentProfileTabContent
                  agent={agent}
                  members={members}
                  currentUserId={currentUserId}
                />
              </div>
            ) : null}
            {renderTab("reminders") && canViewSensitive ? (
              <div className={tab === "reminders" ? undefined : "hidden"}>
                <RemindersTab agent={agent} />
              </div>
            ) : null}
            {renderTab("files") && canViewSensitive ? (
              <div className={tab === "files" ? undefined : "hidden"}>
                <AgentFilesPanel
                  agent={agent}
                  currentUserId={currentUserId}
                  members={members}
                  canReadFiles={canViewSensitive}
                  canEditFiles={isOwner}
                  onClose={onClose}
                  hideHeader
                />
              </div>
            ) : null}
            {renderTab("usage") ? (
              <div className={tab === "usage" ? undefined : "hidden"}>
                <div className="p-3 md:p-4">
                  <AgentUsageSection agent={agent} />
                </div>
              </div>
            ) : null}
          </div>
        </>
      ) : (
        <div className="min-h-0 min-w-0 flex-1 overflow-y-auto">
          <AgentProfileTabContent
            agent={agent}
            members={members}
            currentUserId={currentUserId}
          />
        </div>
      )}
    </ConversationSidePanelShell>
  );
}

function ownerName(agent: Agent, members: readonly MemberWithUser[]): string {
  if (!agent.owner_id) return "—";
  const member = members.find((m) => m.user_id === agent.owner_id);
  return member?.display_name || member?.name || member?.email || agent.owner_id;
}

function formatDate(value: string): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function AgentProfileTabContent({
  agent,
  members,
  currentUserId,
}: {
  agent: Agent;
  members: readonly MemberWithUser[];
  currentUserId: string | null;
}) {
  const { t } = useT("agents");
  const wsId = agent.workspace_id;
  const { data: runtimes = [] } = useQuery(runtimeListOptions(wsId));
  const handleUpdate = useUpdateAgent(wsId);
  const { canEdit, canChangeRole } = useAgentPermissions(agent, wsId);
  const qc = useQueryClient();
  const runtimeHealthLabel = useRuntimeHealthStateLabel();

  // Runtime config used to be editable by any workspace member when the agent
  // carried the `group_manager` marker ("shared team infrastructure"). That
  // marker is retired with the group-manager cutover (#871), and `canEdit`
  // already admits workspace owners/admins, so the special case dissolves
  // rather than needing a replacement.
  const canEditRuntime = canEdit.allowed;
  const canEditIdentity = canEdit.allowed;

  const selectedRuntime = runtimes.find((r) => r.id === agent.runtime_id) ?? null;
  // task #22: gate the profile restart button on the bound runtime's real
  // capability, never a hardcoded provider list. Missing capabilities object
  // (older backend, no runtime bound) means false, not "assume supported".
  const forceRestartSupported =
    selectedRuntime?.provider_capabilities?.force_restart ?? false;
  // Derived, staleness-aware health instead of the raw `status` column
  // (#10 — "runtime online status" had two divergent sources across the
  // app).
  const isOnline =
    !!selectedRuntime && deriveRuntimeHealth(selectedRuntime, Date.now()) === "online";
  const runtimeUpdateHealth =
    agent.runtime_mode !== "cloud" && selectedRuntime ? deriveRuntimeHealthPresentation(selectedRuntime) : "ok";

  const update = (data: Record<string, unknown>) => handleUpdate(agent.id, data);
  const displayName = resolveActorDisplayName(agent, agent.id);
  const [runtimeDialogOpen, setRuntimeDialogOpen] = useState(false);
  const [roleDialogOpen, setRoleDialogOpen] = useState(false);
  const [roleSaving, setRoleSaving] = useState(false);

  const updateWorkspaceRole = async (role: "member" | "admin") => {
    if (role === agent.workspace_role || roleSaving) return;

    const listKey = workspaceKeys.agents(wsId);
    const detailKey = agentDetailKeys.detail(wsId, agent.id);
    const previousRole = agent.workspace_role;
    const patchRole = (workspace_role: "member" | "admin") => {
      qc.setQueryData<Agent[]>(listKey, (current) =>
        current?.map((item) =>
          item.id === agent.id ? { ...item, workspace_role } : item,
        ),
      );
      qc.setQueryData<Agent>(detailKey, (current) =>
        current ? { ...current, workspace_role } : current,
      );
    };

    setRoleSaving(true);
    patchRole(role);
    try {
      await api.updateAgentWorkspaceRole(wsId, agent.id, role);
      toast.success(t(($) => $.profile_card.role_updated));
      setRoleDialogOpen(false);
    } catch (error) {
      patchRole(previousRole);
      showErrorToast(
        error instanceof Error
          ? error.message
          : t(($) => $.profile_card.role_update_failed),
      );
    } finally {
      setRoleSaving(false);
      void qc.invalidateQueries({ queryKey: listKey });
      void qc.invalidateQueries({ queryKey: detailKey });
    }
  };

  return (
    <div className="flex min-w-0 flex-col" data-testid="agent-profile-tab-content">
      <div className="space-y-4 p-3 md:p-4">
        <ProfileField label={t(($) => $.side_panel.display_name_label)}>
          {canEditIdentity ? (
            <InlineFieldEditor
              value={displayName}
              kind="input"
              label={t(($) => $.inspector.display_name_title)}
              placeholder={t(($) => $.inspector.display_name_placeholder)}
              validate={(v) =>
                v.trim().length > 0 ? null : t(($) => $.inspector.display_name_required)
              }
              onSave={(v) => update({ display_name: v.trim() })}
              displayClassName="text-[13px] leading-5"
              testId="agent-profile-display-name"
            />
          ) : (
            <p className="text-[13px] leading-5">{displayName}</p>
          )}
        </ProfileField>

        <ProfileField label={t(($) => $.side_panel.description_label)}>
          {canEditIdentity ? (
            <InlineFieldEditor
              value={agent.description ?? ""}
              kind="textarea"
              label={t(($) => $.side_panel.description_label)}
              placeholder={t(($) => $.inspector.description_placeholder)}
              emptyLabel={t(($) => $.side_panel.no_description)}
              maxLength={AGENT_DESCRIPTION_MAX_LENGTH}
              onSave={(v) => update({ description: v })}
              displayClassName="text-[13px] leading-5 text-foreground/85"
              testId="agent-profile-description"
            />
          ) : (
            <p className="text-[13px] leading-5 text-foreground/85">
              {agent.description || t(($) => $.side_panel.no_description)}
            </p>
          )}
        </ProfileField>

        <AgentHonorPanelSection agentId={agent.id} workspaceId={agent.workspace_id} />

        <div className="border-t border-border pt-3">
          <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t(($) => $.side_panel.info_section)}
          </h3>
          <div className="grid grid-cols-[100px_minmax(0,1fr)] gap-x-3 gap-y-2 text-[13px]">
            <span className="pt-0.5 text-muted-foreground">
              {t(($) => $.profile_card.role_label)}
            </span>
            <div className="flex min-w-0 items-center gap-1">
              <span className="truncate" data-testid="agent-workspace-role-value">
                {agent.workspace_role === "admin" ? "Admin" : "Member"}
              </span>
              {canChangeRole.allowed && !agent.archived_at ? (
                <button
                  type="button"
                  className="inline-flex shrink-0 rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  onClick={() => setRoleDialogOpen(true)}
                  aria-label={t(($) => $.profile_card.role_dialog_title)}
                  data-testid="agent-workspace-role-edit"
                >
                  <Pencil className="size-3.5" aria-hidden />
                </button>
              ) : null}
            </div>
            <span className="text-muted-foreground">{t(($) => $.side_panel.created_label)}</span>
            <span className="truncate" title={formatDate(agent.created_at)}>
              {formatDate(agent.created_at)}
            </span>
            <span className="text-muted-foreground">{t(($) => $.side_panel.owner_label)}</span>
            <span className="truncate" title={ownerName(agent, members)}>
              {ownerName(agent, members)}
            </span>
          </div>
        </div>

        <RolesDialog
          open={roleDialogOpen}
          onOpenChange={setRoleDialogOpen}
          mode="select"
          value={agent.workspace_role}
          allowedRoles={["member", "admin"]}
          saving={roleSaving}
          onSave={(role) => {
            if (role === "owner") {
              return Promise.resolve();
            }
            return updateWorkspaceRole(role);
          }}
          title={t(($) => $.profile_card.role_dialog_title)}
          subtitle={t(($) => $.profile_card.role_dialog_subtitle)}
        />

        {/* LRM-470 — Runtime Config is its own section (not Info misc rows).
            LRM-1351 — summary always shows effective config; edits go through
            a centered Dialog so multi-field changes restart at most once. */}
        <section
          className="border-t border-border pt-3"
          aria-label={t(($) => $.side_panel.runtime_section)}
          data-testid="agent-profile-runtime-config"
        >
          <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t(($) => $.side_panel.runtime_section)}
          </h3>
          {/* LRM-1351 (Frank pencil lock): summary body is not a click target;
              only the trailing pencil opens the Dialog. */}
          <div className="flex items-start gap-1">
            <div className="min-w-0 flex-1">
              <RuntimeConfigSummary
                agent={agent}
                runtimes={runtimes}
                members={members}
                currentUserId={currentUserId}
                isOnline={isOnline}
                runtimeUpdateHealth={runtimeUpdateHealth}
                runtimeHealthLabel={runtimeHealthLabel}
              />
            </div>
            {canEditRuntime ? (
              <button
                type="button"
                className="mt-0.5 inline-flex shrink-0 rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                onClick={() => setRuntimeDialogOpen(true)}
                aria-label={t(($) => $.execution_config.edit_trigger_aria)}
                data-testid="agent-runtime-config-edit"
              >
                <Pencil className="h-3.5 w-3.5" aria-hidden />
              </button>
            ) : null}
          </div>
          <RuntimeConfigDialog
            agent={agent}
            open={runtimeDialogOpen}
            onOpenChange={setRuntimeDialogOpen}
            runtimes={runtimes}
            members={[...members]}
            currentUserId={currentUserId}
            onSave={update}
          />
        </section>

        {agent.memory_growth ? <MemoryGrowthField growth={agent.memory_growth} /> : null}

        <div className="border-t border-border pt-3">
          <AgentProfileActions
            agent={agent}
            canManage={canEdit.allowed}
            forceRestartSupported={forceRestartSupported}
          />
        </div>
      </div>
    </div>
  );
}

function RuntimeConfigSummary({
  agent,
  runtimes,
  members,
  currentUserId,
  isOnline,
  runtimeUpdateHealth,
  runtimeHealthLabel,
}: {
  agent: Agent;
  runtimes: import("@multica/core/types").AgentRuntime[];
  members: readonly MemberWithUser[];
  currentUserId: string | null;
  isOnline: boolean;
  runtimeUpdateHealth: ReturnType<typeof deriveRuntimeHealthPresentation> | "ok";
  runtimeHealthLabel: (health: RuntimeHealthPresentation) => string;
}) {
  const { t } = useT("agents");
  return (
    <>
      <div className="grid grid-cols-[100px_minmax(0,1fr)] gap-x-3 gap-y-2 text-[13px]">
        <span className="pt-0.5 text-muted-foreground">
          {t(($) => $.inspector.prop_computer)}
        </span>
        <ComputerInfoRow
          runtime={
            runtimes.find((r) => r.id === agent.runtime_id) ?? null
          }
        />
        <span className="pt-0.5 text-muted-foreground">
          {t(($) => $.inspector.prop_runtime)}
        </span>
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <RuntimePicker
            value={agent.runtime_id}
            runtimes={runtimes}
            members={[...members]}
            currentUserId={currentUserId}
            canEdit={false}
            onChange={() => {}}
          />
          {runtimeUpdateHealth !== "ok" &&
            runtimeUpdateHealth !== "update_available" && (
              <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                {runtimeHealthLabel(runtimeUpdateHealth)}
              </span>
            )}
          <ModelPicker
            runtimeId={agent.runtime_id}
            runtimeOnline={!!isOnline}
            value={agent.model ?? ""}
            canEdit={false}
            onChange={() => {}}
          />
        </div>
      </div>
      <div className="mt-2 grid min-w-0 grid-cols-[auto_1fr] gap-x-2 gap-y-0.5">
        <ThinkingPropRow
          runtimeId={agent.runtime_id}
          runtimeOnline={!!isOnline}
          model={agent.model ?? ""}
          value={agent.thinking_level ?? ""}
          canEdit={false}
          onChange={() => {}}
        />
      </div>
    </>
  );
}

function ProfileField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
      {children}
    </div>
  );
}

function AgentUsageSection({ agent }: { agent: Agent }) {
  const { t } = useT("agents");
  const timezone = useMemo(() => Intl.DateTimeFormat().resolvedOptions().timeZone, []);
  const usdFormatter = useMemo(
    () =>
      new Intl.NumberFormat(undefined, {
        style: "currency",
        currency: "USD",
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
      }),
    [],
  );
  const { data: allUsage = [], isLoading } = useQuery(
    dashboardUsageByAgentOptions(agent.workspace_id, 30, null, timezone),
  );
  useCustomPricingStore((state) => state.pricings);

  const usage = useMemo(
    () => allUsage.filter((row) => row.agent_id === agent.id),
    [agent.id, allUsage],
  );
  const tokens = useMemo(() => totalTokens(usage), [usage]);
  const cost = useMemo(() => usage.reduce((sum, row) => sum + estimateCost(row), 0), [usage]);
  const canEstimateCost = usage.every((row) => isModelPriced(row.model));

  return (
    <section aria-label={t(($) => $.side_panel.usage_section)}>
      <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        {t(($) => $.side_panel.usage_section)}
        <span className="font-medium normal-case tracking-normal">
          {" "}
          · {t(($) => $.side_panel.usage_reported_window)}
        </span>
      </h3>

      {isLoading ? (
        <p className="text-xs text-muted-foreground">{t(($) => $.side_panel.usage_loading)}</p>
      ) : usage.length === 0 ? (
        <p className="text-xs text-muted-foreground">{t(($) => $.side_panel.usage_empty)}</p>
      ) : (
        <div className="space-y-1.5 text-xs">
          <div className="flex items-baseline justify-between gap-3">
            <span className="text-muted-foreground">{t(($) => $.side_panel.usage_estimated_cost)}</span>
            <span className="text-sm tabular-nums text-foreground">
              {canEstimateCost
                ? usdFormatter.format(cost)
                : t(($) => $.side_panel.usage_cost_unavailable)}
            </span>
          </div>
          <div className="flex items-baseline justify-between gap-3">
            <span className="text-muted-foreground">{t(($) => $.side_panel.usage_tokens)}</span>
            <span className="text-sm tabular-nums text-muted-foreground">{formatTokens(tokens)}</span>
          </div>
        </div>
      )}
    </section>
  );
}

function totalTokens(rows: readonly DashboardUsageByAgent[]): number {
  return rows.reduce(
    (sum, row) =>
      sum + row.input_tokens + row.output_tokens + row.cache_read_tokens + row.cache_write_tokens,
    0,
  );
}
