"use client";

import { ArrowLeft } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { memberListOptions } from "@multica/core/workspace/queries";
import { PageHeader } from "../layout/page-header";
import { useNavigation } from "../navigation";
import { useT } from "../i18n/use-t";
import { ResolvedAgentSidePanel } from "./resolved-agent-side-panel";
import { MemberSidePanel } from "../members/member-side-panel";

/**
 * Mobile full-page host for the actor profile (#586). On mobile, tapping an
 * author/agent avatar routes here instead of opening an 80dvh Drawer that
 * clipped the Recent-activity list. Agents reuse the same owner-gated
 * Profile / Activity / Files tab surface as the conversation side panel
 * (resolved by id via GetAgent — LRM-292); users and unavailable agents
 * retain the generic profile fallback. The agent page keeps the Back/header
 * chrome outside the tab body's scroll container.
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
  const wsPaths = useWorkspacePaths();
  const wsId = useWorkspaceId();
  const currentUserId = useAuthStore((state) => state.user?.id ?? null);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const isAgent = memberType === "agent";

  // LRM-1185 (父 LRM-974 冻 A1): "关闭 = 回到进入前表面". A deep link / hard
  // reload opens this route with no history entry to pop, so a bare `back()`
  // would leave the user stuck on the profile — fall back to the channel list.
  const goBack = () => {
    const canPop =
      typeof window === "undefined" || window.history.length > 1;
    if (canPop) {
      navigation.back();
      return;
    }
    navigation.replace(wsPaths.channels());
  };

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      {/* LRM-1185: leading ← is the primary, discoverable exit on narrow
          screens — 44×44 hit target, 20px glyph, full-contrast label, and top
          padding that clears the notch (safe-area-inset-top). The old chrome
          was a 28px muted text link that read as "there is no close button". */}
      <PageHeader className="h-auto min-h-12 pt-[env(safe-area-inset-top,0px)]">
        <button
          type="button"
          onClick={goBack}
          data-testid="actor-profile-back"
          aria-label={t(($) => $.profile_popover.back)}
          className="-ml-2 inline-flex h-11 min-w-11 items-center gap-1.5 rounded-md px-2 text-sm font-medium text-foreground transition-colors hover:bg-accent"
        >
          <ArrowLeft className="size-5 shrink-0" />
          {t(($) => $.profile_popover.back)}
        </button>
      </PageHeader>
      <div className="flex min-h-0 flex-1">
        <div className="mx-auto flex min-h-0 w-full max-w-2xl flex-1">
          {isAgent ? (
            <ResolvedAgentSidePanel
              agentId={memberId}
              currentUserId={currentUserId}
              members={members}
              onClose={goBack}
              variant="page"
            />
          ) : (
            <MemberSidePanel
              userId={memberId}
              onClose={goBack}
              variant="page"
            />
          )}
        </div>
      </div>
    </div>
  );
}
