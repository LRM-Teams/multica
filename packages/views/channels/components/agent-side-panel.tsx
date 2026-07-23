"use client";

import { type ReactNode, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Activity, BarChart3, Bell, FileText, User } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useConfigStore } from "@multica/core/config";
import { AGENT_DESCRIPTION_MAX_LENGTH } from "@multica/core/agents";
import type { Agent, DashboardUsageByAgent, MemberWithUser } from "@multica/core/types";
import { deriveRuntimeHealthPresentation, runtimeListOptions } from "@multica/core/runtimes";
import { useAgentPermissions } from "@multica/core/permissions";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import {
  formatActorHandleLabel,
  resolveActorDisplayName,
  resolveActorHandle,
} from "@multica/core/identity";
import { dashboardUsageByAgentOptions } from "@multica/core/dashboard/queries";
import { useCustomPricingStore } from "@multica/core/runtimes/custom-pricing-store";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { cn } from "@multica/ui/lib/utils";
import { ActivityTab } from "../../agents/components/tabs/activity-tab";
import { RemindersTab } from "../../agents/components/tabs/reminders-tab";
import { AgentXpBurst } from "../../agents/components/agent-xp-burst";
import { ModelPicker } from "../../agents/components/inspector/model-picker";
import { RuntimePicker } from "../../agents/components/inspector/runtime-picker";
import { ThinkingPropRow } from "../../agents/components/inspector/thinking-prop-row";
import { VisibilityPicker } from "../../agents/components/inspector/visibility-picker";
import { MemoryGrowthField } from "../../agents/components/memory-growth-field";
import { AgentProfileActions } from "../../agents/components/agent-profile-actions";
import { InlineFieldEditor } from "../../agents/components/inline-field-editor";
import { useUpdateAgent } from "../../agents/hooks/use-update-agent";
import { useRuntimeHealthStateLabel } from "../../runtimes/components/shared";
import { initialsOf } from "../../common/initials";
import { ConversationSidePanelShell } from "../../common/conversation-side-panel-shell";
import { AgentPresenceOverlay } from "../../common/actor-avatar";
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
}: AgentSidePanelProps) {
  const { t } = useT("agents");
  const isOwner = !!currentUserId && agent.owner_id === currentUserId;
  const devProfileAccess = useConfigStore((state) => state.agentProfileDevAccessEnabled);
  const canInspectAgent = isOwner || (!!currentUserId && devProfileAccess);
  const isWorkspaceMember =
    !!currentUserId && members.some((member) => member.user_id === currentUserId);
  const isGroupManager = agent.managed_role === "group_manager";
  const canViewActivity =
    isOwner ||
    (!!currentUserId && devProfileAccess) ||
    (isWorkspaceMember && agent.visibility === "workspace") ||
    (isWorkspaceMember && isGroupManager);
  const availableTabs: OwnerTab[] = ["profile"];
  if (canViewActivity) availableTabs.push("activity");
  if (canViewActivity) availableTabs.push("reminders");
  if (canInspectAgent) availableTabs.push("files");
  // LRM-448: Usage is always a direct tab (never stacked in Profile).
  availableTabs.push("usage");
  const showTabBar = availableTabs.length > 1;
  const [tab, setTab] = useState<OwnerTab>("profile");
  const [mountedTabs, setMountedTabs] = useState<Set<OwnerTab>>(() => new Set(["profile"]));
  const tabBodyRef = useRef<HTMLDivElement>(null);
  const tabScrollTopRef = useRef<Partial<Record<OwnerTab, number>>>({});
  const displayName = resolveActorDisplayName(agent, agent.id);
  const handleLabel = formatActorHandleLabel(resolveActorHandle(agent));
  const initials = initialsOf(displayName);
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

  // LRM-448: header chrome is Close-only. Identity lives below (Computer IA).
  const leading = useMemo(() => <span className="sr-only">{displayName}</span>, [displayName]);

  return (
    <ConversationSidePanelShell
      variant={variant}
      onClose={onClose}
      closeAriaLabel={t(($) => $.side_panel.close_aria)}
      leading={leading}
    >
      <div
        className={cn(
          "flex shrink-0 items-start gap-3 px-4 pb-3 pt-1",
          variant === "page" && "px-0",
        )}
        data-testid="agent-profile-identity"
      >
        <AgentXpBurst agentId={agent.id}>
          <AgentPresenceOverlay agentId={agent.id} size={56}>
            <ActorAvatarBase
              name={displayName}
              initials={initials}
              avatarUrl={resolvePublicFileUrl(agent.avatar_url)}
              isAgent
              size={56}
              className={agent.archived_at ? "opacity-50 grayscale" : undefined}
            />
          </AgentPresenceOverlay>
        </AgentXpBurst>
        <div className="min-w-0 flex-1 pt-0.5">
          <p className="truncate text-lg font-bold leading-tight">{displayName}</p>
          <p className="mt-1 truncate text-xs text-muted-foreground">
            {handleLabel || `@${agent.name}`}
            {agent.archived_at ? (
              <span className="ml-2">{t(($) => $.row.archived)}</span>
            ) : null}
          </p>
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
            {renderTab("activity") && canViewActivity ? (
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
            {renderTab("reminders") && canViewActivity ? (
              <div className={tab === "reminders" ? undefined : "hidden"}>
                <RemindersTab agent={agent} />
              </div>
            ) : null}
            {renderTab("files") && canInspectAgent ? (
              <div className={tab === "files" ? undefined : "hidden"}>
                <AgentFilesPanel
                  agent={agent}
                  currentUserId={currentUserId}
                  members={members}
                  canReadFiles={canInspectAgent}
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
  const { canEdit } = useAgentPermissions(agent, wsId);
  const runtimeHealthLabel = useRuntimeHealthStateLabel();

  const isGroupManager = agent.managed_role === "group_manager";
  const canEditRuntime = isGroupManager ? true : canEdit.allowed;
  const canEditIdentity = canEdit.allowed;

  const selectedRuntime = runtimes.find((r) => r.id === agent.runtime_id) ?? null;
  const isOnline = selectedRuntime?.status === "online";
  const runtimeUpdateHealth =
    agent.runtime_mode !== "cloud" && selectedRuntime ? deriveRuntimeHealthPresentation(selectedRuntime) : "ok";

  const update = (data: Record<string, unknown>) => handleUpdate(agent.id, data);
  const displayName = resolveActorDisplayName(agent, agent.id);

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

        <div className="border-t border-border pt-3">
          <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t(($) => $.side_panel.info_section)}
          </h3>
          <div className="grid grid-cols-[100px_minmax(0,1fr)] gap-x-3 gap-y-2 text-[13px]">
            <span className="text-muted-foreground">{t(($) => $.side_panel.role_label)}</span>
            <span>
              <span className="inline-flex rounded-md border border-brand/30 bg-brand/10 px-2 py-0.5 text-xs font-semibold text-brand">
                {t(($) => $.side_panel.role_agent)}
              </span>
            </span>
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

        {/* LRM-470 — Runtime Config is its own section (not Info misc rows). */}
        <section
          className="border-t border-border pt-3"
          aria-label={t(($) => $.side_panel.runtime_section)}
          data-testid="agent-profile-runtime-config"
        >
          <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t(($) => $.side_panel.runtime_section)}
          </h3>
          <div className="grid grid-cols-[100px_minmax(0,1fr)] gap-x-3 gap-y-2 text-[13px]">
            <span className="pt-0.5 text-muted-foreground">
              {t(($) => $.inspector.prop_runtime)}
            </span>
            <div className="flex min-w-0 flex-wrap items-center gap-1.5">
              <RuntimePicker
                value={agent.runtime_id}
                runtimes={runtimes}
                members={[...members]}
                currentUserId={currentUserId}
                canEdit={canEditRuntime}
                onChange={(id) => update({ runtime_id: id })}
              />
              {runtimeUpdateHealth !== "ok" && (
                <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                  {runtimeHealthLabel(runtimeUpdateHealth)}
                </span>
              )}
              <ModelPicker
                runtimeId={agent.runtime_id}
                runtimeOnline={!!isOnline}
                value={agent.model ?? ""}
                canEdit={canEditRuntime}
                onChange={(m) => update({ model: m })}
              />
              <VisibilityPicker
                value={agent.visibility}
                homeChannelId={agent.home_channel_id ?? null}
                canEdit={canEditRuntime}
                onChange={(next) =>
                  update({
                    visibility: next.visibility,
                    home_channel_id: next.home_channel_id,
                  })
                }
              />
            </div>
          </div>
          <div className="mt-2 grid min-w-0 grid-cols-[auto_1fr] gap-x-2 gap-y-0.5">
            <ThinkingPropRow
              runtimeId={agent.runtime_id}
              runtimeOnline={!!isOnline}
              model={agent.model ?? ""}
              value={agent.thinking_level ?? ""}
              canEdit={canEditRuntime}
              onChange={(v) => update({ thinking_level: v })}
            />
          </div>
          {canEditRuntime ? (
            <p className="mt-2 text-[10px] leading-tight text-muted-foreground">
              {t(($) => $.execution_config.applies_next_run)}
            </p>
          ) : null}
        </section>

        {agent.memory_growth ? <MemoryGrowthField growth={agent.memory_growth} /> : null}

        <div className="border-t border-border pt-3">
          <AgentProfileActions
            agent={agent}
            canManage={canEdit.allowed}
          />
        </div>
      </div>
    </div>
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
