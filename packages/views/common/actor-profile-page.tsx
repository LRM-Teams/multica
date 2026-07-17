"use client";

import { ArrowLeft } from "lucide-react";
import { PageHeader } from "../layout/page-header";
import { useNavigation } from "../navigation";
import { useT } from "../i18n/use-t";
import { ActorProfileContent } from "./actor-profile-popover";

/**
 * Mobile full-page host for the actor profile (#586). On mobile, tapping an
 * author/agent avatar routes here instead of opening an 80dvh Drawer that
 * clipped the Recent-activity list. It renders the SAME shared peek content
 * (`ActorProfileContent` → identity + compact `ActivityTimeline`) full-width,
 * so the whole page scrolls vertically, under a header with a Back button.
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
      <div className="flex-1 min-h-0 overflow-y-auto">
        <div className="mx-auto w-full max-w-2xl">
          <ActorProfileContent memberType={memberType} memberId={memberId} />
        </div>
      </div>
    </div>
  );
}
