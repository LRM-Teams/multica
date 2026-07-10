"use client";

import { useQuery } from "@tanstack/react-query";
import type { Agent, AgentRuntime } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  deriveRuntimeHealth,
  runtimeHealthState,
  type RuntimeHealth,
} from "@multica/core/runtimes";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { resolveActorDisplayName } from "@multica/core/identity";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { MessageSquare } from "lucide-react";
import { AppLink } from "../../navigation/app-link";
import { useOpenDM } from "../../common/use-open-dm";
import {
  HealthIcon,
  useRuntimeHealthStateLabel,
} from "../../runtimes/components/shared";
import { VisibilityBadge } from "./visibility-badge";
import { AgentPresenceStatusLine } from "./agent-presence-status-line";
import { useT } from "../../i18n/use-t";

interface AgentProfileCardProps {
  agentId: string;
}

export function AgentProfileCard({ agentId }: AgentProfileCardProps) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const p = useWorkspacePaths();
  const { openDM, isPending: openingDM } = useOpenDM();
  const { data: agents = [], isLoading: agentsLoading } = useQuery(agentListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: runtimes = [] } = useQuery(runtimeListOptions(wsId));

  const agent = agents.find((a) => a.id === agentId);

  if (agentsLoading && !agent) {
    return (
      <div className="flex items-center gap-3">
        <Skeleton className="h-10 w-10 rounded-full" />
        <div className="flex-1 space-y-1.5">
          <Skeleton className="h-4 w-28" />
          <Skeleton className="h-3 w-20" />
        </div>
      </div>
    );
  }

  if (!agent) {
    return (
      <div className="text-xs text-muted-foreground">{t(($) => $.profile_card.unavailable)}</div>
    );
  }

  const owner = agent.owner_id
    ? members.find((m) => m.user_id === agent.owner_id) ?? null
    : null;
  const runtime = runtimes.find((r) => r.id === agent.runtime_id) ?? null;
  const isArchived = !!agent.archived_at;
  const displayName = resolveActorDisplayName(agent, agent.id);
  const initials = displayName
    .split(" ")
    .map((w) => w[0])
    .join("")
    .toUpperCase()
    .slice(0, 2);

  return (
    // `group` enables the hover-only Detail link on the top-right —
    // it fades in only when the user is hovering the card chrome,
    // staying out of the way during a quick glance.
    <div className="group flex flex-col gap-3 text-left">
      {/* Header — avatar + name + availability on the left, "Detail →" link
          on the right (hover-only). Card stays minimal: only the 3-state
          availability dot is surfaced here; last-task state lives in the
          agents list and the agent detail page. */}
      <div className="flex items-start gap-3">
        <ActorAvatarBase
          name={displayName}
          initials={initials}
          avatarUrl={resolvePublicFileUrl(agent.avatar_url)}
          isAgent
          size={40}
          className="rounded-md"
        />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <ActorIdentityRow identity={agent} primaryClassName="truncate text-sm font-semibold" className="min-w-0 shrink" />
            {!isArchived && <VisibilityBadge value={agent.visibility} compact />}
            {isArchived && (
              <span className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                {t(($) => $.row.archived)}
              </span>
            )}
          </div>
          {!isArchived && <AgentAvailabilityLine agentId={agent.id} />}

        </div>
        {!isArchived && (
          <div className="mr-1 mt-0.5 flex shrink-0 items-center gap-2 opacity-0 transition-opacity group-hover:opacity-100">
            <button
              type="button"
              disabled={openingDM}
              onClick={() => void openDM({ peer_type: "agent", peer_id: agent.id })}
              className="inline-flex items-center gap-1 text-xs font-normal text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50"
            >
              <MessageSquare className="size-3.5" />
              {t(($) => $.profile_card.send_message)}
            </button>
            <AppLink
              href={p.agentDetail(agent.id)}
              className="text-xs font-normal text-brand"
            >
              {t(($) => $.profile_card.detail_link)}
            </AppLink>
          </div>
        )}
      </div>

      {/* Description */}
      {agent.description && (
        <p className="line-clamp-2 text-xs text-muted-foreground">
          {agent.description}
        </p>
      )}

      {/* Meta rows — minimal set: runtime (where it lives), skills (what
          it knows), owner (who manages it). Model is intentionally
          omitted — power-user detail lives on the detail page. */}
      <div className="flex flex-col gap-1.5 text-xs">
        <RuntimeRow agent={agent} runtime={runtime} />
        {agent.skills.length > 0 && (
          <SkillsRow skills={agent.skills.map((s) => s.name)} />
        )}
        {owner && <MetaRow label={t(($) => $.profile_card.owner_label)} value={owner.name} />}
      </div>
    </div>
  );
}

// Live name-row status under the agent name — same mark as the profile
// hover card / DM header (dot + word via useAgentLiveStatus). Coarse when
// idle; stage-detail when a task is active.
function AgentAvailabilityLine({ agentId }: { agentId: string }) {
  return (
    <div className="mt-0.5">
      <AgentPresenceStatusLine agentId={agentId} className="max-w-[12rem]" />
    </div>
  );
}

// Compact runtime row — wifi-style health icon + runtime name. The icon
// shape (Wifi / WifiOff) plus colour reflects the live runtime health
// derived from runtime + clock; cloud runtimes always read as online.
// This is duplicate signal with the availability dot above by design —
// the dot is the agent's effective availability (which mostly tracks
// runtime health), and seeing the same wifi icon next to the runtime
// name confirms WHICH runtime is the one currently in the dot's state.
function RuntimeRow({
  agent,
  runtime,
}: {
  agent: Agent;
  runtime: AgentRuntime | null;
}) {
  const { t } = useT("agents");
  const runtimeHealthLabel = useRuntimeHealthStateLabel();
  const isCloud = agent.runtime_mode === "cloud";
  const health: RuntimeHealth = isCloud
    ? "online"
    : runtime
      ? deriveRuntimeHealth(runtime, Date.now())
      : "offline";
  const updateHealth = !isCloud && runtime ? runtimeHealthState(runtime) : "ok";
  const label =
    runtime?.name ??
    (isCloud
      ? t(($) => $.row.fallback_runtime_cloud)
      : t(($) => $.profile_card.unknown_runtime));
  return (
    <div className="flex items-center gap-1.5">
      <span className="w-12 shrink-0 text-muted-foreground">{t(($) => $.profile_card.runtime_label)}</span>
      <HealthIcon health={health} className="h-3 w-3 shrink-0" />
      <span className="min-w-0 truncate" title={label}>
        {label}
      </span>
      {updateHealth !== "ok" && (
        <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
          {runtimeHealthLabel(updateHealth)}
        </span>
      )}
    </div>
  );
}

function MetaRow({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="flex items-center gap-1.5">
      <span className="w-12 shrink-0 text-muted-foreground">{label}</span>
      <span className={`truncate ${mono ? "font-mono text-[11px]" : ""}`} title={value}>
        {value}
      </span>
    </div>
  );
}

function SkillsRow({ skills }: { skills: string[] }) {
  const { t } = useT("agents");
  const visible = skills.slice(0, 3);
  const overflow = skills.length - visible.length;
  return (
    <div className="flex items-center gap-1.5">
      <span className="w-12 shrink-0 text-muted-foreground">{t(($) => $.profile_card.skills_label)}</span>
      <div className="flex min-w-0 flex-wrap gap-1">
        {visible.map((s) => (
          <span
            key={s}
            className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground"
          >
            {s}
          </span>
        ))}
        {overflow > 0 && (
          <span className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
            +{overflow}
          </span>
        )}
      </div>
    </div>
  );
}
