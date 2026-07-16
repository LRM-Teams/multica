"use client";

import { type ReactNode, useState } from "react";
import { Activity, FileText, Settings, User, X } from "lucide-react";
import { toast } from "sonner";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useConfigStore } from "@multica/core/config";
import type { Agent, MemberWithUser, UpdateAgentRequest } from "@multica/core/types";
import { api } from "@multica/core/api";
import { runtimeListOptions } from "@multica/core/runtimes";
import { workspaceKeys } from "@multica/core/workspace/queries";
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
import { PropRow } from "../../common/prop-row";
import { initialsOf } from "../../common/initials";
import { AgentFilesPanel } from "./agent-files-panel";
import { useT } from "../../i18n/use-t";

type OwnerTab = "activity" | "profile" | "files" | "config";

const TAB_ICONS: Record<OwnerTab, typeof Activity> = {
  profile: User,
  activity: Activity,
  files: FileText,
  config: Settings,
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
 */
export function AgentSidePanel({ agent, currentUserId, members, onClose }: AgentSidePanelProps) {
  const { t } = useT("agents");
  const isOwner = !!currentUserId && agent.owner_id === currentUserId;
  const devProfileAccess = useConfigStore((state) => state.agentProfileDevAccessEnabled);
  const canInspectAgent = isOwner || (!!currentUserId && devProfileAccess);
  // Beckham (per-group manager) is shared team infrastructure: expose a
  // runtime config tab that ANY workspace member can edit, regardless of the
  // owner-only inspect gate above.
  const isGroupManager = agent.managed_role === "group_manager";
  const availableTabs: OwnerTab[] = ["profile"];
  if (canInspectAgent) availableTabs.push("activity", "files");
  if (isGroupManager) availableTabs.push("config");
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
            {tab === "profile" && <AgentProfileTabContent agent={agent} members={members} />}
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
            {tab === "config" && isGroupManager && (
              <GroupManagerConfigTab
                agent={agent}
                members={members}
                currentUserId={currentUserId}
              />
            )}
          </div>
        </>
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <AgentProfileTabContent agent={agent} members={members} />
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

function AgentProfileTabContent({
  agent,
  members,
}: {
  agent: Agent;
  members: readonly MemberWithUser[];
}) {
  const { t } = useT("agents");
  // Which runtime this agent runs on (Frank: raft shows it, so show it here too).
  // Read straight off the aggregated agent payload — the BE denormalizes
  // `runtime_name` alongside `runtime_health`/`runtime_mode` (#534), so no
  // separate runtime-list lookup. Cloud agents show the localized "Cloud" label;
  // a local runtime with no resolved name reads as "—".
  const runtimeValue =
    agent.runtime_mode === "cloud"
      ? t(($) => $.side_panel.runtime_cloud)
      : agent.runtime_name?.trim() || "—";
  return (
    <div className="flex flex-col">
      <div className="border-b p-4">
        <p className="text-xs leading-5 text-foreground/85">
          {agent.description || t(($) => $.side_panel.no_description)}
        </p>
      </div>
      <div className="space-y-2 border-b p-4 text-xs">
        <InfoRow label={t(($) => $.side_panel.model_label)} value={agent.model} mono />
        <InfoRow
          label={t(($) => $.side_panel.reasoning_label)}
          value={agent.thinking_level?.trim() || t(($) => $.side_panel.reasoning_default)}
        />
        <InfoRow label={t(($) => $.side_panel.runtime_label)} value={runtimeValue} />
        <InfoRow label={t(($) => $.side_panel.created_label)} value={formatDate(agent.created_at)} />
        <InfoRow label={t(($) => $.side_panel.owner_label)} value={ownerName(agent, members)} />
      </div>
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

/**
 * Runtime config surface for a per-group manager (Beckham), shown as a tab in
 * the channel-side agent panel. Reuses the agent-detail inspector pickers.
 * Editable by ANY workspace member — group managers are shared infrastructure
 * (the backend gate at canManageAgent allows any member for group_manager
 * agents), so `canEdit` is always true here. Ordinary agents never render
 * this tab (the parent only mounts it for managed_role === "group_manager").
 */
function GroupManagerConfigTab({
  agent,
  members,
  currentUserId,
}: {
  agent: Agent;
  members: readonly MemberWithUser[];
  currentUserId: string | null;
}) {
  const { t } = useT("agents");
  const qc = useQueryClient();
  const wsId = agent.workspace_id;
  const { data: runtimes = [] } = useQuery(runtimeListOptions(wsId));
  const selectedRuntime = runtimes.find((r) => r.id === agent.runtime_id) ?? null;
  const isOnline = selectedRuntime?.status === "online";

  // Optimistic patch of the cached agents list (mirrors the detail page's
  // handleUpdate) so picker chips flip immediately; rollback only the fields
  // this call wrote on failure, then invalidate to reconcile with the server.
  const handleUpdate = async (data: Record<string, unknown>) => {
    const queryKey = workspaceKeys.agents(wsId);
    const prevAgents = qc.getQueryData<Agent[]>(queryKey);
    const prevAgent = prevAgents?.find((a) => a.id === agent.id);
    const prevFields: Record<string, unknown> = {};
    if (prevAgent) {
      for (const key of Object.keys(data)) {
        prevFields[key] = (prevAgent as unknown as Record<string, unknown>)[key];
      }
    }
    qc.setQueryData<Agent[]>(queryKey, (old) =>
      old?.map((a) => (a.id === agent.id ? ({ ...a, ...data } as Agent) : a)),
    );
    try {
      await api.updateAgent(agent.id, data as UpdateAgentRequest);
      qc.invalidateQueries({ queryKey });
    } catch (e) {
      if (prevAgent) {
        qc.setQueryData<Agent[]>(queryKey, (old) =>
          old?.map((a) => (a.id === agent.id ? ({ ...a, ...prevFields } as Agent) : a)),
        );
      }
      qc.invalidateQueries({ queryKey });
      toast.error(e instanceof Error ? e.message : t(($) => $.detail.update_failed_toast));
      throw e;
    }
  };

  return (
    <div className="flex flex-col">
      <div className="border-b p-4">
        <p className="text-xs leading-5 text-muted-foreground">
          {t(($) => $.side_panel.config_shared_hint)}
        </p>
      </div>
      <ConfigSection label={t(($) => $.inspector.section_properties)}>
        <PropRow label={t(($) => $.inspector.prop_runtime)} interactive={false}>
          <RuntimePicker
            value={agent.runtime_id}
            runtimes={runtimes}
            members={[...members]}
            currentUserId={currentUserId}
            canEdit
            onChange={(id) => handleUpdate({ runtime_id: id })}
          />
        </PropRow>
        <PropRow label={t(($) => $.inspector.prop_model)} interactive={false}>
          <ModelPicker
            runtimeId={agent.runtime_id}
            runtimeOnline={!!isOnline}
            value={agent.model ?? ""}
            canEdit
            onChange={(m) => handleUpdate({ model: m })}
          />
        </PropRow>
        <ThinkingPropRow
          runtimeId={agent.runtime_id}
          runtimeOnline={!!isOnline}
          model={agent.model ?? ""}
          value={agent.thinking_level ?? ""}
          canEdit
          onChange={(v) => handleUpdate({ thinking_level: v })}
        />
        <PropRow label={t(($) => $.inspector.prop_visibility)} interactive={false}>
          <VisibilityPicker
            value={agent.visibility}
            canEdit
            onChange={(v) => handleUpdate({ visibility: v })}
          />
        </PropRow>
        <PropRow label={t(($) => $.inspector.prop_concurrency)} interactive={false}>
          <ConcurrencyPicker
            value={agent.max_concurrent_tasks}
            canEdit
            onChange={(n) => handleUpdate({ max_concurrent_tasks: n })}
          />
        </PropRow>
      </ConfigSection>
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
