"use client";

import { type ReactNode, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Activity, FileText, User, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useConfigStore } from "@multica/core/config";
import type { Agent, DashboardUsageByAgent, MemberWithUser } from "@multica/core/types";
import { runtimeHealthState, runtimeListOptions } from "@multica/core/runtimes";
import { useAgentPermissions } from "@multica/core/permissions";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { resolveActorDisplayName } from "@multica/core/identity";
import { dashboardUsageByAgentOptions } from "@multica/core/dashboard/queries";
import { useCustomPricingStore } from "@multica/core/runtimes/custom-pricing-store";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { ActivityTab } from "../../agents/components/tabs/activity-tab";
import { AgentPresenceStatusLine } from "../../agents/components/agent-presence-status-line";
import { ModelPicker } from "../../agents/components/inspector/model-picker";
import { RuntimePicker } from "../../agents/components/inspector/runtime-picker";
import { ThinkingPropRow } from "../../agents/components/inspector/thinking-prop-row";
import { VisibilityPicker } from "../../agents/components/inspector/visibility-picker";
import { useUpdateAgent } from "../../agents/hooks/use-update-agent";
import { useRuntimeHealthStateLabel } from "../../runtimes/components/shared";
import { PropRow } from "../../common/prop-row";
import { initialsOf } from "../../common/initials";
import { AgentFilesPanel } from "./agent-files-panel";
import { useT } from "../../i18n/use-t";
import { estimateCost, formatTokens, isModelPriced } from "../../runtimes/utils";

type OwnerTab = "activity" | "profile" | "files";

const TAB_ICONS: Record<OwnerTab, typeof Activity> = {
  profile: User,
  activity: Activity,
  files: FileText,
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
 * Production keeps Frank's original privacy correction: owner sees
 * Profile/Activity/Files, non-owner sees Profile only. Dev deployments can
 * open Activity/Files read surfaces for workspace members via /api/config.
 *
 * The former standalone "Config" tab was merged into Profile (#565): the same
 * runtime attributes were shown read-only in Profile AND editable in Config —
 * one interface lying about the other. Profile now carries an identity section
 * (read-only) plus a runtime-config section (editable/gated) in one place.
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
  const availableTabs: OwnerTab[] = ["profile"];
  if (canInspectAgent) availableTabs.push("activity", "files");
  const showTabBar = availableTabs.length > 1;
  const [tab, setTab] = useState<OwnerTab>("profile");
  const [mountedTabs, setMountedTabs] = useState<Set<OwnerTab>>(() => new Set(["profile"]));
  const tabBodyRef = useRef<HTMLDivElement>(null);
  const tabScrollTopRef = useRef<Partial<Record<OwnerTab, number>>>({});
  const displayName = resolveActorDisplayName(agent, agent.id);
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

  // Page tabs share a single scroll container. Save before hiding the current
  // tab and restore after its sibling becomes visible, otherwise a short
  // Profile tab clamps Activity's history position back to the top.
  useLayoutEffect(() => {
    if (variant !== "page" || !tabBodyRef.current) return;
    tabBodyRef.current.scrollTop = tabScrollTopRef.current[tab] ?? 0;
  }, [tab, variant]);

  return (
    <aside
      className={cn(
        "flex h-full min-h-0 min-w-0 w-full flex-col bg-background",
        variant === "panel" && "border-l",
      )}
    >
      <div className="flex items-center justify-between gap-3 border-b p-4">
        <div className="flex min-w-0 items-center gap-2.5">
          <ActorAvatarBase
            name={displayName}
            initials={initials}
            avatarUrl={resolvePublicFileUrl(agent.avatar_url)}
            isAgent
            size={32}
          />
          {/* #371: name + live presence tight together (matches DM header /
              profile hover card). Visible before opening the Activity tab. */}
          <div className="flex min-w-0 items-center gap-2">
            <p className="min-w-0 truncate text-sm font-semibold">{displayName}</p>
            <AgentPresenceStatusLine agentId={agent.id} className="max-w-[9rem]" />
          </div>
        </div>
        {variant === "panel" ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={onClose}
            aria-label={t(($) => $.side_panel.close_aria)}
          >
            <X className="size-4" />
          </Button>
        ) : null}
      </div>

      {showTabBar ? (
        <>
          <div
            className={cn(
              "flex shrink-0 items-center gap-0 border-b px-2",
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
            {renderTab("activity") && canInspectAgent ? (
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
    </aside>
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
  return date.toLocaleString();
}

/**
 * Profile tab: an IDENTITY section (read-only description / created / owner)
 * followed by a RUNTIME-CONFIG section (editable/gated pickers) — the merge of
 * the old Profile + Config tabs (#565).
 *
 * Permission split (the one real risk — no privilege widening): the runtime
 * pickers are editable when `canEditRuntime` is true. For a per-group manager
 * (Beckham) that is ALWAYS true — group managers are shared team
 * infrastructure and the backend `canUpdateAgent` gate lets any member edit
 * these five runtime fields (identity + lifecycle via `canManageAgent` stays
 * owner/admin only). This preserves the old Config-tab behavior. For every other agent it
 * falls back to `useAgentPermissions(agent).canEdit.allowed`, i.e. owner /
 * workspace-admin only — so an ordinary non-owner viewer keeps a READ-ONLY
 * Profile (the inspector pickers self-render static when `canEdit=false`).
 * Identity fields stay read-only for everyone here (name/description/
 * instructions editing lives on the owner/admin-gated detail page).
 */
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

  const selectedRuntime = runtimes.find((r) => r.id === agent.runtime_id) ?? null;
  const isOnline = selectedRuntime?.status === "online";
  // Runtime "version outdated" badge (#527): an INDEPENDENT axis from
  // online/offline health. Cloud runtimes never report an outdated local
  // binary. Reuses the exact logic + label from the profile hover card.
  const runtimeUpdateHealth =
    agent.runtime_mode !== "cloud" && selectedRuntime ? runtimeHealthState(selectedRuntime) : "ok";

  const update = (data: Record<string, unknown>) => handleUpdate(agent.id, data);

  return (
    // min-w-0 so nothing inside can force the panel wider than its column —
    // the docked panel is 360-440px on desktop and a ~90vw / full-width sheet
    // on mobile (breakpoint 768). Every leaf below truncates instead.
    <div className="flex min-w-0 flex-col">
      {/* Identity (read-only): who / what this agent is. Tighter horizontal
          padding under md (mobile) to give the values more room at 375px. */}
      <div className="border-b p-3 md:p-4">
        <p className="text-xs leading-5 text-foreground/85">
          {agent.description || t(($) => $.side_panel.no_description)}
        </p>
      </div>
      <div className="space-y-2 border-b p-3 text-xs md:p-4">
        <InfoRow label={t(($) => $.side_panel.created_label)} value={formatDate(agent.created_at)} />
        <InfoRow label={t(($) => $.side_panel.owner_label)} value={ownerName(agent, members)} />
      </div>

      <AgentUsageSection agent={agent} />

      {/* Runtime config (editable/gated): the execution attributes the old
          standalone Config tab exposed, merged here so Profile no longer shows
          them read-only while a separate tab edited them. */}
      <ConfigSection label={t(($) => $.side_panel.runtime_section)}>
        <PropRow label={t(($) => $.inspector.prop_runtime)} interactive={false}>
          {/* flex-wrap so the version-outdated (过期) badge drops below the
              runtime chip instead of being squeezed off at 375px — it must
              stay visible per Barry's mobile check. */}
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
          </div>
        </PropRow>
        <PropRow label={t(($) => $.inspector.prop_model)} interactive={false}>
          <ModelPicker
            runtimeId={agent.runtime_id}
            runtimeOnline={!!isOnline}
            value={agent.model ?? ""}
            canEdit={canEditRuntime}
            onChange={(m) => update({ model: m })}
          />
        </PropRow>
        <ThinkingPropRow
          runtimeId={agent.runtime_id}
          runtimeOnline={!!isOnline}
          model={agent.model ?? ""}
          value={agent.thinking_level ?? ""}
          canEdit={canEditRuntime}
          onChange={(v) => update({ thinking_level: v })}
        />
        <PropRow label={t(($) => $.inspector.prop_visibility)} interactive={false}>
          <VisibilityPicker
            value={agent.visibility}
            canEdit={canEditRuntime}
            onChange={(v) => update({ visibility: v })}
          />
        </PropRow>
      </ConfigSection>
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
  // estimateCost resolves custom rates outside React; subscribe so the card
  // recomputes when the viewer changes one, matching the Usage dashboard.
  useCustomPricingStore((state) => state.pricings);

  const usage = useMemo(
    () => allUsage.filter((row) => row.agent_id === agent.id),
    [agent.id, allUsage],
  );
  const tokens = useMemo(() => totalTokens(usage), [usage]);
  const cost = useMemo(() => usage.reduce((sum, row) => sum + estimateCost(row), 0), [usage]);
  const canEstimateCost = usage.every((row) => isModelPriced(row.model));

  return (
    <section className="border-b px-3 py-4 md:px-4" aria-label={t(($) => $.side_panel.usage_section)}>
      <div className="mb-3">
        <h3 className="text-sm font-medium text-foreground">{t(($) => $.side_panel.usage_section)}</h3>
        <p className="mt-0.5 text-xs text-muted-foreground">
          {t(($) => $.side_panel.usage_reported_window)}
        </p>
      </div>

      {isLoading ? (
        <p className="text-xs text-muted-foreground">{t(($) => $.side_panel.usage_loading)}</p>
      ) : usage.length === 0 ? (
        <p className="text-xs text-muted-foreground">{t(($) => $.side_panel.usage_empty)}</p>
      ) : (
        <div className="space-y-2 text-xs">
          <div className="flex items-baseline justify-between gap-3">
            <span className="text-muted-foreground">{t(($) => $.side_panel.usage_estimated_cost)}</span>
            <span className="text-base font-semibold tabular-nums text-foreground">
              {canEstimateCost
                ? usdFormatter.format(cost)
                : t(($) => $.side_panel.usage_cost_unavailable)}
            </span>
          </div>
          <div className="flex items-baseline justify-between gap-3">
            <span className="text-muted-foreground">{t(($) => $.side_panel.usage_tokens)}</span>
            <span className="tabular-nums text-muted-foreground">{formatTokens(tokens)}</span>
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

function InfoRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[72px_minmax(0,1fr)] gap-2 md:grid-cols-[88px_minmax(0,1fr)]">
      <span className="text-muted-foreground">{label}</span>
      <span className={cn("truncate text-foreground", mono && "font-mono")} title={value}>
        {value}
      </span>
    </div>
  );
}

function ConfigSection({ label, children }: { label: string; children: ReactNode }) {
  return (
    // Tighter horizontal padding under md (mobile) widens the picker column at
    // 375px; the auto/1fr grid keeps the label + picker on one row (same info
    // hierarchy at every breakpoint), with every picker chip truncating.
    <div className="border-b px-3 py-4 md:px-4">
      <div className="mb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      <div className="grid min-w-0 grid-cols-[auto_1fr] gap-x-2 gap-y-0.5">{children}</div>
    </div>
  );
}
