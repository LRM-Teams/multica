"use client";

import { ArrowLeft } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { AgentSidePanel } from "../channels/components/agent-side-panel";
import { PageHeader } from "../layout/page-header";
import { useNavigation } from "../navigation";
import { useT } from "../i18n/use-t";
import { ActorProfileContent } from "./actor-profile-popover";

/**
 * Mobile full-page host for the actor profile (#586). On mobile, tapping an
 * author/agent avatar routes here instead of opening an 80dvh Drawer that
 * clipped the Recent-activity list. Agents reuse the same owner-gated
 * Profile / Activity / Files tab surface as the conversation side panel;
 * users and unavailable agents retain the generic profile fallback. The agent
 * page keeps the Back/header chrome outside the tab body's scroll container.
 *
 * This is intentionally NOT the agent management page (`AgentDetailPage`): it is
 * the lightweight, actor-generic profile for both agents and users.
 */
export function ActorProfilePage({
  memberType,
  memberId,
}: {
  memberType: "agent" | "user";
  memberId: string;
}) {
  const { t } = useT("channels");
  const navigation = useNavigation();
  const wsId = useWorkspaceId();
  const currentUserId = useAuthStore((state) => state.user?.id ?? null);
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const agent = memberType === "agent" ? agents.find((candidate) => candidate.id === memberId) : null;

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader>
        <button
          type="button"
          onClick={() => navigation.back()}
          className="inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          {t(($) => $.profile_popover.back)}
        </button>
      </PageHeader>
      <div className={agent ? "flex min-h-0 flex-1" : "min-h-0 flex-1 overflow-y-auto"}>
        <div
          className={
            agent
              ? "mx-auto flex min-h-0 w-full max-w-2xl flex-1"
              : "mx-auto w-full max-w-2xl"
          }
        >
          {agent ? (
            <AgentSidePanel
              agent={agent}
              currentUserId={currentUserId}
              members={members}
              onClose={() => navigation.back()}
              variant="page"
            />
          ) : (
            <ActorProfileContent memberType={memberType} memberId={memberId} />
          )}
        </div>
      </div>
    </div>
  );
}
