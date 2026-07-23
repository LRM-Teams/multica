"use client";

import { useQuery } from "@tanstack/react-query";
import { dmListOptions } from "@multica/core/dm";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { DmConversation } from "../../channels/components/dm-conversation";
import { useT } from "../../i18n";

/**
 * Activity right-pane DM shell (LRM-388). Resolves the DM row then mounts the
 * shared `<DmConversation>` (Chat default). Missing DM is an explicit error —
 * no silent stub peer (LRM-238).
 */
export function ActivityDmPane({
  channelId,
  deepLinkMessageId,
  threadDeepLinkId,
  onBack,
}: {
  channelId: string;
  deepLinkMessageId?: string | null;
  threadDeepLinkId?: string | null;
  onBack: () => void;
}) {
  const { t } = useT("inbox");
  const wsId = useWorkspaceId();
  const { data: dms = [], isLoading, isError, refetch } = useQuery(
    dmListOptions(wsId),
  );
  const dm = dms.find((item) => item.id === channelId) ?? null;

  if (isLoading && !dm) {
    return (
      <div className="flex h-full flex-col gap-3 p-4">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-4 w-32" />
        <Skeleton className="mt-4 h-24 w-full" />
      </div>
    );
  }

  if (isError || !dm) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
        <p className="text-sm text-destructive">
          {t(($) => $.activity.open_dm_failed)}
        </p>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            {t(($) => $.activity.retry)}
          </Button>
          <Button variant="ghost" size="sm" onClick={onBack}>
            {t(($) => $.page.back)}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <DmConversation
      dm={dm}
      onBack={onBack}
      deepLinkMessageId={deepLinkMessageId}
      threadDeepLinkId={threadDeepLinkId}
    />
  );
}
