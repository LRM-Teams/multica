"use client";

import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { dmListOptions } from "@multica/core/dm";
import type { DMItem } from "@multica/core/dm";
import { useWorkspaceId } from "@multica/core/hooks";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT, useTimeAgo } from "../../i18n";

/**
 * DIRECT MESSAGES sidebar region — the top half of the unified Messages
 * sidebar (GROUPS sits below). Fed by `GET /api/dm`, which unions kind='dm'
 * channels with legacy chat_sessions and is already recency-sorted, so we
 * preserve the server order (unread / new float up). The header is collapsible
 * and, when collapsed, surfaces the aggregate unread count.
 *
 * Selection is unified with groups by the parent: `activeId` is the currently
 * open conversation id regardless of region, so opening a DM clears the group
 * selection and vice-versa.
 */
export function DmList({
  activeId,
  currentUserName,
  onSelect,
}: {
  /** Currently open conversation id (DM or group) — drives row highlight. */
  activeId: string | null;
  /** Viewer's display name, used to detect mentions in the last-message preview. */
  currentUserName: string | null;
  onSelect: (dm: DMItem) => void;
}) {
  const { t } = useT("channels");
  const timeAgo = useTimeAgo();
  const wsId = useWorkspaceId();
  const { data: dms = [], isLoading } = useQuery(dmListOptions(wsId));
  const [collapsed, setCollapsed] = useState(false);

  const aggregateUnread = useMemo(
    () => dms.reduce((sum, dm) => sum + (dm.unread ?? 0), 0),
    [dms],
  );

  return (
    <div className="pb-1">
      <button
        type="button"
        onClick={() => setCollapsed((c) => !c)}
        className="flex w-full items-center gap-1 px-2 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
        aria-expanded={!collapsed}
      >
        {collapsed ? (
          <ChevronRight className="size-3.5 shrink-0" />
        ) : (
          <ChevronDown className="size-3.5 shrink-0" />
        )}
        <span className="flex-1 text-left">{t(($) => $.dm.heading)}</span>
        {collapsed && aggregateUnread > 0 && (
          <span className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-medium text-primary-foreground">
            {aggregateUnread > 99 ? "99+" : aggregateUnread}
          </span>
        )}
      </button>

      {!collapsed &&
        (isLoading ? (
          <div className="space-y-2 p-2">
            <Skeleton className="h-12" />
            <Skeleton className="h-12" />
          </div>
        ) : dms.length === 0 ? (
          <div className="px-3 py-2 text-sm text-muted-foreground">
            {t(($) => $.dm.empty)}
          </div>
        ) : (
          dms.map((dm) => (
            <DmRow
              key={`${dm.source}:${dm.id}`}
              dm={dm}
              active={activeId === dm.id}
              currentUserName={currentUserName}
              timeAgo={timeAgo}
              onSelect={() => onSelect(dm)}
            />
          ))
        ))}
    </div>
  );
}

function DmRow({
  dm,
  active,
  currentUserName,
  timeAgo,
  onSelect,
}: {
  dm: DMItem;
  active: boolean;
  currentUserName: string | null;
  timeAgo: (dateStr: string) => string;
  onSelect: () => void;
}) {
  const last = dm.last_message;
  const preview = last ? `${last.author_name}: ${last.content}`.replace(/\s+/g, " ") : "";
  // Surface mentions of the viewer at full foreground weight (no bold) so an
  // @-mention reads as more salient than ordinary preview text.
  const mentionsUser =
    !!last &&
    !!currentUserName &&
    last.content.toLowerCase().includes(`@${currentUserName.toLowerCase()}`);
  const unread = dm.unread ?? 0;
  // peer.type "user" maps to the member-style avatar; agents get the presence
  // status dot. Both resolve name/avatar from the workspace queries.
  const actorType = dm.peer.type === "agent" ? "agent" : "member";

  return (
    <div
      className={cn(
        "mb-0.5 rounded-lg transition-colors",
        active ? "bg-primary/[0.08]" : "hover:bg-accent",
      )}
    >
      <button
        type="button"
        onClick={onSelect}
        className="flex w-full items-center gap-2.5 rounded-lg px-2 py-2 text-left"
      >
        <ActorAvatar
          actorType={actorType}
          actorId={dm.peer.id}
          size={40}
          showStatusDot={dm.peer.type === "agent"}
          profileLink={false}
        />
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <span className="truncate text-sm font-medium text-foreground">
              {dm.peer.name}
            </span>
            {last && (
              <span className="shrink-0 text-[11px] text-muted-foreground">
                {timeAgo(last.created_at)}
              </span>
            )}
          </div>
          <div className="mt-0.5 flex items-center justify-between gap-2">
            <span
              className={cn(
                "truncate text-xs",
                mentionsUser ? "text-foreground" : "text-muted-foreground",
              )}
            >
              {preview}
            </span>
            {unread > 0 && (
              <span className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-medium text-primary-foreground">
                {unread > 99 ? "99+" : unread}
              </span>
            )}
          </div>
        </div>
      </button>
    </div>
  );
}
