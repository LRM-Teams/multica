"use client";

import { useQuery } from "@tanstack/react-query";
import type { Agent, MemberRole } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core";
import { agentRunCounts30dOptions } from "@multica/core/agents";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { resolveActorDisplayName } from "@multica/core/identity";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import { useWorkspacePaths } from "@multica/core/paths";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { ActorIdentityRow } from "../common/actor-identity-row";
import { ActorAvatar } from "../common/actor-avatar";
import { useOpenAgentPanel } from "../common/agent-panel-context";
import { AppLink } from "../navigation";
import { useT } from "../i18n";

interface MemberProfileCardProps {
  // The User UUID — matches member.user_id and agent.owner_id. We accept user_id
  // (not member.id) because every existing call site passes user_id (assignee_id,
  // commenter_id, owner_id are all User UUIDs in the polymorphic actor model).
  userId: string;
}

// Mirrors AgentProfileCard's structure so the two hover surfaces feel like
// twins ("agent and human are both first-class team members"). Content is
// asymmetric on purpose: humans get identity + the AI agents they own; they
// don't get a status dot because there's no member-presence backbone today
// and we don't want to fabricate one.
export function MemberProfileCard({ userId }: MemberProfileCardProps) {
  const { t } = useT("members");
  const wsId = useWorkspaceId();
  const { data: members = [], isLoading: membersLoading } = useQuery(
    memberListOptions(wsId),
  );
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: runCounts = [] } = useQuery(agentRunCounts30dOptions(wsId));

  const member = members.find((m) => m.user_id === userId);

  if (membersLoading && !member) {
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

  if (!member) {
    return (
      <div className="text-xs text-muted-foreground">{t(($) => $.card.unavailable)}</div>
    );
  }

  // Sort owned agents by 30-day run count (most-used first); break ties on
  // name for a stable order. Run counts come from the same workspace-wide
  // query that powers the Agents-list RUNS column — no extra fetch.
  const runCountById = new Map(runCounts.map((r) => [r.agent_id, r.run_count]));
  const ownedAgents = agents
    .filter((a) => a.owner_id === userId && !a.archived_at)
    .sort((a, b) => {
      const ra = runCountById.get(a.id) ?? 0;
      const rb = runCountById.get(b.id) ?? 0;
      if (ra !== rb) return rb - ra;
      return resolveActorDisplayName(a, a.id).localeCompare(resolveActorDisplayName(b, b.id));
    });

  return (
    <div className="flex flex-col gap-3 text-left">
      {/* Header */}
      <div className="flex items-start gap-3">
        <ActorAvatar
          actorType="member"
          actorId={userId}
          size={40}
          avatarUrlHint={member.avatar_url}
          profileLink={false}
          className="rounded-full"
        />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <ActorIdentityRow
              identity={member}
              primaryClassName="truncate text-sm font-semibold"
            />
            <RoleBadge role={member.role} />
          </div>
          <p className="mt-0.5 truncate text-xs text-muted-foreground">{member.email}</p>
        </div>
      </div>

      {/* Owned agents */}
      {ownedAgents.length > 0 && (
        <OwnedAgentsSection userId={userId} agents={ownedAgents} />
      )}
    </div>
  );
}

function RoleBadge({ role }: { role: MemberRole }) {
  const { t } = useT("members");
  return (
    <span className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
      {role === "owner"
        ? t(($) => $.role.owner)
        : role === "admin"
          ? t(($) => $.role.admin)
          : t(($) => $.role.member)}
    </span>
  );
}

function OwnedAgentsSection({
  userId,
  agents,
}: {
  userId: string;
  agents: Agent[];
}) {
  const { t } = useT("members");
  // LRM-877 — in-session / hover: push Agent onto Dock Stack (panel), not
  // hard-link to /agents/:id management page. Deep links stay for new-tab.
  const p = useWorkspacePaths();
  const openAgentFromContext = useOpenAgentPanel();
  const openAgentFromStore = useAgentPanelStore((s) => s.open);
  const openAgent = openAgentFromContext ?? openAgentFromStore;

  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <span className="text-muted-foreground">{t(($) => $.card.agents_section, { count: agents.length })}</span>
      <div className="flex flex-col gap-0.5">
        {agents.map((a) => {
          const body = (
            <>
              <ActorAvatar
                actorType="agent"
                actorId={a.id}
                size={20}
                showStatusDot
                profileLink={false}
                className="mt-0.5 shrink-0 rounded-md"
              />
              <div className="min-w-0 flex-1">
                <ActorIdentityRow identity={a} primaryClassName="truncate font-medium" />
                {a.description && (
                  <div className="truncate text-muted-foreground">
                    {a.description}
                  </div>
                )}
              </div>
              <span
                aria-hidden
                className="mt-0.5 shrink-0 font-normal text-brand opacity-0 transition-opacity group-hover:opacity-100"
              >
                {t(($) => $.card.detail_link)}
              </span>
            </>
          );
          if (openAgent) {
            return (
              <button
                key={a.id}
                type="button"
                data-testid="member-card-owned-agent"
                onClick={() =>
                  openAgent(
                    a.id,
                    {
                      name: a.name,
                      display_name: a.display_name,
                      avatar_url: a.avatar_url,
                    },
                    { returnToMemberId: userId },
                  )
                }
                className="group -mx-1 flex w-full cursor-pointer items-start gap-2 rounded-md px-1 py-1 text-left transition-colors hover:bg-accent"
              >
                {body}
              </button>
            );
          }
          return (
            <AppLink
              key={a.id}
              href={p.agentDetail(a.id)}
              className="group -mx-1 flex cursor-pointer items-start gap-2 rounded-md px-1 py-1 transition-colors hover:bg-accent"
            >
              {body}
            </AppLink>
          );
        })}
      </div>
    </div>
  );
}
