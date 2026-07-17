"use client";

import { type ReactNode, useState } from "react";
import { Activity, FileText, User, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useConfigStore } from "@multica/core/config";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { runtimeHealthState, runtimeListOptions } from "@multica/core/runtimes";
import { useAgentPermissions } from "@multica/core/permissions";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { resolveActorDisplayName } from "@multica/core/identity";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { ActivityTab } from "../../agents/components/tabs/activity-tab";
import { AgentPresenceStatusLine } from "../../agents/components/agent-presence-status-line";
import { ConcurrencyPicker } from "../../agents/components/inspector/concurrency-picker";
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
export function AgentSidePanel({ agent, currentUserId, members, onClose }: AgentSidePanelProps) {
  const { t } = useT("agents");
  const isOwner = !!currentUserId && agent.owner_id === currentUserId;
  const devProfileAccess = useConfigStore((state) => state.agentProfileDevAccessEnabled);
  const canInspectAgent = isOwner || (!!currentUserId && devProfileAccess);
  const availableTabs: OwnerTab[] = ["profile"];
  if (canInspectAgent) availableTabs.push("activity", "files");
  const showTabBar = availableTabs.length > 1;
  const [tab, setTab] = useState<OwnerTab>("profile");
  const displayName = resolveActorDisplayName(agent, agent.id);
  const initials = initialsOf(displayName);

  return (
    <aside className="flex h-full min-h-0 flex-col border-l bg-background">
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
        <Button
          type="button"
          variant="ghost"
          size="icon"
          onClick={onClose}
          aria-label={t(($) => $.side_panel.close_aria)}
        >
          <X className="size-4" />
        </Button>
      </div>

      {showTabBar ? (
        <>
          <div className="flex shrink-0 items-center gap-0 border-b px-2">
            {availableTabs.map((tabId) => {
              const Icon = TAB_ICONS[tabId];
              return (
                <button
                  key={tabId}
                  type="button"
                  onClick={() => setTab(tabId)}
                  className={cn(
                    "flex shrink-0 items-center gap-1.5 whitespace-nowrap border-b-2 px-3 py-2.5 text-xs font-medium transition-colors",
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
          <div className="min-h-0 flex-1 overflow-y-auto">
            {tab === "activity" && canInspectAgent && <ActivityTab agent={agent} />}
            {tab === "profile" && (
              <AgentProfileTabContent
                agent={agent}
                members={members}
                currentUserId={currentUserId}
              />
            )}
            {tab === "files" && canInspectAgent && (
              <AgentFilesPanel
                agent={agent}
                currentUserId={currentUserId}
                members={members}
                canReadFiles={canInspectAgent}
                canEditFiles={isOwner}
                onClose={onClose}
                hideHeader
              />
            )}
          </div>
        </>
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto">
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
 * infrastructure and the backend `canManageAgent` gate lets any member edit
 * them (this preserves the old Config-tab behavior). For every other agent it
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
    <div className="flex flex-col">
      {/* Identity (read-only): who / what this agent is. */}
      <div className="border-b p-4">
        <p className="text-xs leading-5 text-foreground/85">
          {agent.description || t(($) => $.side_panel.no_description)}
        </p>
      </div>
      <div className="space-y-2 border-b p-4 text-xs">
        <InfoRow label={t(($) => $.side_panel.created_label)} value={formatDate(agent.created_at)} />
        <InfoRow label={t(($) => $.side_panel.owner_label)} value={ownerName(agent, members)} />
      </div>

      {/* Runtime config (editable/gated): the execution attributes the old
          standalone Config tab exposed, merged here so Profile no longer shows
          them read-only while a separate tab edited them. */}
      <ConfigSection label={t(($) => $.inspector.section_properties)}>
        <PropRow label={t(($) => $.inspector.prop_runtime)} interactive={false}>
          <div className="flex min-w-0 items-center gap-1.5">
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
        <PropRow label={t(($) => $.inspector.prop_concurrency)} interactive={false}>
          <ConcurrencyPicker
            value={agent.max_concurrent_tasks}
            canEdit={canEditRuntime}
            onChange={(n) => update({ max_concurrent_tasks: n })}
          />
        </PropRow>
      </ConfigSection>
    </div>
  );
}

function InfoRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[88px_minmax(0,1fr)] gap-2">
      <span className="text-muted-foreground">{label}</span>
      <span className={cn("truncate text-foreground", mono && "font-mono")} title={value}>
        {value}
      </span>
    </div>
  );
}

function ConfigSection({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="border-b px-4 py-4">
      <div className="mb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      <div className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5">{children}</div>
    </div>
  );
}
