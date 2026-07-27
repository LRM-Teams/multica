"use client";

import { ArrowLeft } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { memberListOptions } from "@multica/core/workspace/queries";
import { PageHeader } from "../layout/page-header";
import { useNavigation } from "../navigation";
import { useT } from "../i18n/use-t";
import { ResolvedAgentSidePanel } from "./resolved-agent-side-panel";
import { HumanMemberSidePanel } from "../members/human-member-side-panel";

/**
 * Mobile full-page host for the actor profile (#586). On mobile, tapping an
 * author/agent avatar routes here instead of opening an 80dvh Drawer that
 * clipped the Recent-activity list. Agents reuse the same owner-gated
 * Profile / Activity / Files tab surface as the conversation side panel
 * (resolved by id via GetAgent — LRM-292); humans use the LRM-619 Lock A
 * five-section profile. The agent page keeps the Back/header chrome outside
 * the tab body's scroll container.
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
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const isAgent = memberType === "agent";

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
      <div className={isAgent ? "flex min-h-0 flex-1" : "min-h-0 flex-1 overflow-hidden"}>
        <div
          className={
            isAgent
              ? "mx-auto flex min-h-0 w-full max-w-2xl flex-1"
              : "mx-auto flex min-h-0 w-full max-w-2xl flex-1"
          }
        >
          {isAgent ? (
            <ResolvedAgentSidePanel
              agentId={memberId}
              currentUserId={currentUserId}
              members={members}
              onClose={() => navigation.back()}
              variant="page"
            />
          ) : (
            <HumanMemberSidePanel
              userId={memberId}
              onClose={() => navigation.back()}
              variant="page"
            />
          )}
        </div>
      </div>
    </div>
  );
}
