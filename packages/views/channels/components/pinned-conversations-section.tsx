"use client";

import { useMemo, type ReactNode } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useWorkspaceId } from "@multica/core/hooks";
import { useT } from "../../i18n/use-t";
import { useSidebarSectionCollapsed } from "../hooks/use-sidebar-section-collapsed";
import {
  isConversationMuted,
  sumUnmutedUnreadCounts,
} from "./conversation-muted";
import type { PinnedConversationEntry } from "./pinned-conversations";
import { CONVERSATION_SIDEBAR_UNREAD_BADGE } from "./conversation-sidebar-styles";

/**
 * Slack-style unified PINNED section at the top of the Messages sidebar.
 * Owns collapse + aggregate unread only; callers render each entry row.
 */
export function PinnedConversationsSection({
  entries,
  children,
}: {
  entries: PinnedConversationEntry[];
  children: ReactNode;
}) {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  // LRM-655: same remount-safe collapse as DMs / CHANNELS.
  const [collapsed, setCollapsed] = useSidebarSectionCollapsed("pinned", wsId);

  const aggregateUnread = useMemo(() => {
    let total = 0;
    total += sumUnmutedUnreadCounts(
      entries.flatMap((e) => (e.kind === "dm" ? [e.dm] : [])),
      (dm) => dm.real_unread ?? dm.unread ?? 0,
      (dm) => isConversationMuted(dm),
    );
    total += sumUnmutedUnreadCounts(
      entries.flatMap((e) => (e.kind === "channel" ? [e.channel] : [])),
      (c) => c.real_unread_count ?? c.unread_count ?? 0,
      (c) => isConversationMuted(c),
    );
    return total;
  }, [entries]);

  if (entries.length === 0) return null;

  return (
    <div className="pb-1" data-section="pinned">
      <div className="flex items-center gap-0.5 px-2 py-1.5">
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className="flex flex-1 items-center gap-1 text-xs font-medium uppercase tracking-wide text-muted-foreground transition-colors hover:text-foreground"
          aria-expanded={!collapsed}
        >
          {collapsed ? (
            <ChevronRight className="size-3.5 shrink-0" />
          ) : (
            <ChevronDown className="size-3.5 shrink-0" />
          )}
          <span className="flex-1 text-left">{t(($) => $.sidebar.pinned_section)}</span>
          {collapsed && aggregateUnread > 0 && (
            <span className={CONVERSATION_SIDEBAR_UNREAD_BADGE}>
              {aggregateUnread > 99 ? "99+" : aggregateUnread}
            </span>
          )}
        </button>
      </div>
      {!collapsed && children}
    </div>
  );
}
